package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Poster struct {
	UserID            string `json:"userId"`
	FullName          string `json:"fullName"`
	AvatarURL         string `json:"avatarUrl"`
	AvatarThumbURL    string `json:"avatarThumbUrl"`
	KycStatus         string `json:"kycStatus"`
	IsBusinessAccount bool   `json:"isBusinessAccount"`
}

type Category struct {
	ID          string `json:"_id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

type Gig struct {
	ID             string    `json:"_id"`
	Poster         Poster    `json:"poster"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	CategoryID     Category  `json:"categoryId"`
	BudgetAmount   int       `json:"budgetAmount"`
	GigMode        string    `json:"gigMode"`
	SlotCount      int       `json:"slotCount"`
	FilledSlots    int       `json:"filledSlots"`
	CompletedSlots int       `json:"completedSlots"`
	Currency       string    `json:"currency"`
	LocationType   string    `json:"locationType"`
	City           string    `json:"city"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	ScheduledAt    time.Time `json:"scheduledAt"`
}

type APIResponse struct {
	Data []Gig `json:"data"`
}

const (
	apiURL          = "https://api.sidegigx.id/api/v1/gigs?feedMode=explore&sort=latest&page=1&limit=10"
	pollInterval    = 1 * time.Minute
	revivedThreshold = 3 // blacklist otomatis setelah bangkit sebanyak N kali
)

var knownGigIDs = make(map[string]bool)
var allGigs = make(map[string]Gig)
var lastSeenAt = make(map[string]time.Time) // kapan terakhir gig ini ada di API
var activeGigIDs = make(map[string]bool)    // gig yang ADA di response terakhir
var revivedCounts = make(map[string]int)    // berapa kali gig ini bangkit kembali
var blacklist = make(map[string]bool)       // gig yang sudah diblacklist
var telegramBotToken string

var telegramChatIDs = []string{"1131652151", "1809470127"}

func fetchGigs() ([]Gig, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gagal buat request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "SideGigX-Monitor/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal fetch API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status tidak OK: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gagal baca response body: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("gagal parse JSON: %w", err)
	}

	return apiResp.Data, nil
}

func checkForNewGigs() {
	now := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("\n[%s] 🔍 Mengecek gig terbaru...\n", now)

	gigs, err := fetchGigs()
	if err != nil {
		log.Printf("❌ Error: %v\n", err)
		return
	}

	fmt.Printf("📦 Total gig dari API: %d\n", len(gigs))

	newCount := 0
	revivedCount := 0
	skippedBlacklist := 0

	currentIDs := make(map[string]bool)
	for _, gig := range gigs {
		currentIDs[gig.ID] = true
	}

	for _, gig := range gigs {
		// Skip gig yang sudah diblacklist
		if blacklist[gig.ID] {
			skippedBlacklist++
			continue
		}

		wasActive := activeGigIDs[gig.ID]
		isNew := !knownGigIDs[gig.ID]

		if isNew {
			// Gig benar-benar baru, belum pernah kelihatan sebelumnya
			knownGigIDs[gig.ID] = true
			allGigs[gig.ID] = gig
			lastSeenAt[gig.ID] = time.Now()
			newCount++
			printNewGig(gig)
			if err := sendTelegramNotification(gig); err != nil {
				log.Printf("❌ Gagal kirim Telegram: %v\n", err)
			}
		} else if !wasActive {
			// Gig lama yang sempat hilang dari API, sekarang muncul lagi
			revivedCounts[gig.ID]++
			allGigs[gig.ID] = gig
			lastSeenAt[gig.ID] = time.Now()
			revivedCount++

			if revivedCounts[gig.ID] >= revivedThreshold {
				// Sudah bangkit terlalu sering — masukkan blacklist
				blacklist[gig.ID] = true
				printBlacklisted(gig, revivedCounts[gig.ID])
				if err := sendTelegramNotificationBlacklisted(gig, revivedCounts[gig.ID]); err != nil {
					log.Printf("❌ Gagal kirim Telegram (blacklist): %v\n", err)
				}
			} else {
				printRevivedGig(gig, revivedCounts[gig.ID])
				if err := sendTelegramNotificationRevived(gig, revivedCounts[gig.ID]); err != nil {
					log.Printf("❌ Gagal kirim Telegram (revived): %v\n", err)
				}
			}
		} else {
			// Gig masih aktif seperti biasa
			allGigs[gig.ID] = gig
			lastSeenAt[gig.ID] = time.Now()
		}
	}

	activeGigIDs = currentIDs

	if newCount == 0 {
		fmt.Println("✅ Tidak ada gig baru.")
	} else {
		fmt.Printf("🆕 Ditemukan %d gig baru!\n", newCount)
	}
	if revivedCount > 0 {
		fmt.Printf("♻️  Ditemukan %d gig bangkit kembali!\n", revivedCount)
	}
	if skippedBlacklist > 0 {
		fmt.Printf("🚫 Dilewati (blacklist): %d gig\n", skippedBlacklist)
	}

	fmt.Printf("📊 Total gig di memori: %d | Blacklist: %d\n", len(allGigs), len(blacklist))
}

func printRevivedGig(g Gig, count int) {
	fmt.Println("─────────────────────────────────────────")
	fmt.Printf("♻️  GIG BANGKIT KEMBALI (ke-%d) — threshold: %d\n", count, revivedThreshold)
	fmt.Printf("   ID       : %s\n", g.ID)
	fmt.Printf("   Judul    : %s\n", g.Title)
	fmt.Printf("   Poster   : %s\n", g.Poster.FullName)
	fmt.Printf("   Budget   : %s %d\n", g.Currency, g.BudgetAmount)
	fmt.Printf("   Status   : %s\n", g.Status)
	fmt.Println("─────────────────────────────────────────")
}

func printBlacklisted(g Gig, count int) {
	fmt.Println("─────────────────────────────────────────")
	fmt.Printf("🚫 GIG DIBLACKLIST (bangkit %d kali, melebihi threshold %d)\n", count, revivedThreshold)
	fmt.Printf("   ID       : %s\n", g.ID)
	fmt.Printf("   Judul    : %s\n", g.Title)
	fmt.Printf("   Poster   : %s\n", g.Poster.FullName)
	fmt.Println("─────────────────────────────────────────")
}

func printNewGig(g Gig) {
	fmt.Println("─────────────────────────────────────────")
	fmt.Printf("🆕 GIG BARU DITEMUKAN!\n")
	fmt.Printf("   ID       : %s\n", g.ID)
	fmt.Printf("   Judul    : %s\n", g.Title)
	fmt.Printf("   Deskrips : %s\n", g.Description)
	fmt.Printf("   Poster   : %s\n", g.Poster.FullName)
	fmt.Printf("   Kategori : %s\n", g.CategoryID.Name)
	fmt.Printf("   Budget   : %s %d\n", g.Currency, g.BudgetAmount)
	fmt.Printf("   Status   : %s\n", g.Status)
	fmt.Printf("   Dibuat   : %s\n", g.CreatedAt.Local().Format("02 Jan 2006 15:04:05"))
	fmt.Println("─────────────────────────────────────────")
}

func sendTelegramMessages(text string) error {
	if telegramBotToken == "" {
		return nil
	}
	var errs []string
	apiEndpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", telegramBotToken)
	for _, chatID := range telegramChatIDs {
		values := url.Values{}
		values.Set("chat_id", chatID)
		values.Set("text", text)
		values.Set("parse_mode", "HTML")

		resp, err := http.PostForm(apiEndpoint, values)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", chatID, err))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			errs = append(errs, fmt.Sprintf("%s: %d %s", chatID, resp.StatusCode, string(body)))
		}
		resp.Body.Close()
	}
	if len(errs) > 0 {
		return fmt.Errorf("gagal kirim ke: %s", strings.Join(errs, "; "))
	}
	return nil
}

func sendTelegramNotification(g Gig) error {
	text := fmt.Sprintf("🆕 <b>Gig Baru!</b>\n📌 <b>%s</b>\n📝 %s\n💰 %s %d\n👤 %s\n🏙️ %s",
		g.Title, g.Description, g.Currency, g.BudgetAmount, g.Poster.FullName, g.City,
	)
	return sendTelegramMessages(text)
}

func sendTelegramNotificationRevived(g Gig, count int) error {
	text := fmt.Sprintf("♻️ <b>Gig Tersedia Lagi!</b> (ke-%d, batas: %d)\n📌 <b>%s</b>\n📝 %s\n💰 %s %d\n👤 %s\n🏙️ %s",
		count, revivedThreshold,
		g.Title, g.Description, g.Currency, g.BudgetAmount, g.Poster.FullName, g.City,
	)
	return sendTelegramMessages(text)
}

func sendTelegramNotificationBlacklisted(g Gig, count int) error {
	text := fmt.Sprintf("🚫 <b>Gig Diblacklist</b>\nBangkit %d kali, tidak akan dinotifikasi lagi.\n📌 <b>%s</b>\n👤 %s",
		count, g.Title, g.Poster.FullName,
	)
	return sendTelegramMessages(text)
}

func main() {
	telegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	if telegramBotToken == "" {
		log.Println("⚠️  TELEGRAM_BOT_TOKEN tidak diset. Notifikasi Telegram akan dinonaktifkan.")
	}

	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║   SideGigX Monitor - Mulai Jalan!    ║")
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Printf("🔗 Endpoint  : %s\n", apiURL)
	fmt.Printf("⏱️  Interval  : setiap %s\n", pollInterval)
	fmt.Printf("🚫 Threshold : blacklist setelah bangkit %dx\n\n", revivedThreshold)

	checkForNewGigs()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for range ticker.C {
		checkForNewGigs()
	}
}