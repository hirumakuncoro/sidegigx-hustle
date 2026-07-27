package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
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
	apiURL           = "https://api.sidegigx.id/api/v1/gigs?feedMode=explore&sort=latest&page=1&limit=10"
	appGigURL        = "https://app.sidegigx.id/gig"
	pollInterval     = 1 * time.Minute
	revivedThreshold = 2 // blacklist otomatis setelah gig muncul lagi sebanyak N kali
)

var telegramBotToken string

var telegramChatIDs = []string{"1131652151", "6494495144", "1809470127"}

type StoredGig struct {
	Gig          Gig
	Active       bool
	RevivedCount int
	Blacklisted  bool
}

type Store struct {
	db *sql.DB
}

func NewStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("gagal buka database: %w", err)
	}

	store := &Store{db: db}
	if err := store.init(); err != nil {
		db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) init() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS gigs (
			id TEXT PRIMARY KEY,
			gig_json TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 0,
			revived_count INTEGER NOT NULL DEFAULT 0,
			blacklisted INTEGER NOT NULL DEFAULT 0
		);
	`)
	if err != nil {
		return fmt.Errorf("gagal init database: %w", err)
	}
	return nil
}

func (s *Store) GetGig(id string) (StoredGig, bool, error) {
	var stored StoredGig
	var gigJSON string
	var active, blacklisted int

	err := s.db.QueryRow(`
		SELECT gig_json, active, revived_count, blacklisted
		FROM gigs
		WHERE id = ?
	`, id).Scan(&gigJSON, &active, &stored.RevivedCount, &blacklisted)
	if err == sql.ErrNoRows {
		return StoredGig{}, false, nil
	}
	if err != nil {
		return StoredGig{}, false, fmt.Errorf("gagal ambil gig %s: %w", id, err)
	}
	if err := json.Unmarshal([]byte(gigJSON), &stored.Gig); err != nil {
		return StoredGig{}, false, fmt.Errorf("gagal parse gig %s dari database: %w", id, err)
	}

	stored.Active = active == 1
	stored.Blacklisted = blacklisted == 1
	return stored, true, nil
}

func (s *Store) SaveGig(gig Gig, active bool, revivedCount int, blacklisted bool) error {
	gigJSON, err := json.Marshal(gig)
	if err != nil {
		return fmt.Errorf("gagal encode gig %s: %w", gig.ID, err)
	}

	_, err = s.db.Exec(`
		INSERT INTO gigs (id, gig_json, last_seen_at, active, revived_count, blacklisted)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			gig_json = excluded.gig_json,
			last_seen_at = excluded.last_seen_at,
			active = excluded.active,
			revived_count = excluded.revived_count,
			blacklisted = excluded.blacklisted
	`, gig.ID, string(gigJSON), time.Now().Format(time.RFC3339), boolToInt(active), revivedCount, boolToInt(blacklisted))
	if err != nil {
		return fmt.Errorf("gagal simpan gig %s: %w", gig.ID, err)
	}

	return nil
}

func (s *Store) ReplaceActiveGigs(ids map[string]bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("gagal mulai transaksi: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE gigs SET active = 0`); err != nil {
		return fmt.Errorf("gagal reset gig aktif: %w", err)
	}

	stmt, err := tx.Prepare(`UPDATE gigs SET active = 1 WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("gagal siapkan update gig aktif: %w", err)
	}
	defer stmt.Close()

	for id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			return fmt.Errorf("gagal update gig aktif %s: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("gagal commit gig aktif: %w", err)
	}

	return nil
}

func (s *Store) Counts() (total int, blacklisted int, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(blacklisted), 0) FROM gigs`).Scan(&total, &blacklisted)
	if err != nil {
		return 0, 0, fmt.Errorf("gagal hitung gig: %w", err)
	}
	return total, blacklisted, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func gigLink(id string) string {
	return fmt.Sprintf("%s/%s", appGigURL, id)
}

func randomFakeIP() string {
	return fmt.Sprintf("%d.%d.%d.%d",
		rand.Intn(223)+1,
		rand.Intn(256),
		rand.Intn(256),
		rand.Intn(256),
	)
}

func fetchGigs() ([]Gig, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	fakeIP := randomFakeIP()

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gagal buat request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "SideGigX-Monitor/1.0")
	req.Header.Set("X-Forwarded-For", fakeIP)
	req.Header.Set("X-Real-IP", fakeIP)

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

func checkForNewGigs(store *Store) {
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
		stored, exists, err := store.GetGig(gig.ID)
		if err != nil {
			log.Printf("❌ Gagal baca database: %v\n", err)
			continue
		}

		// Skip gig yang sudah diblacklist
		if stored.Blacklisted {
			skippedBlacklist++
			continue
		}

		wasActive := stored.Active
		isNew := !exists

		if isNew {
			// Gig benar-benar baru, belum pernah kelihatan sebelumnya
			if err := store.SaveGig(gig, true, 0, false); err != nil {
				log.Printf("❌ Gagal simpan gig baru: %v\n", err)
				continue
			}
			newCount++
			printNewGig(gig)
			if err := sendTelegramNotification(gig); err != nil {
				log.Printf("❌ Gagal kirim Telegram: %v\n", err)
			}
		} else if !wasActive {
			// Gig lama yang sempat hilang dari API, sekarang muncul lagi
			revivedCountForGig := stored.RevivedCount + 1
			blacklisted := revivedCountForGig >= revivedThreshold
			if err := store.SaveGig(gig, true, revivedCountForGig, blacklisted); err != nil {
				log.Printf("❌ Gagal simpan gig revived: %v\n", err)
				continue
			}
			revivedCount++

			if blacklisted {
				// Sudah bangkit terlalu sering — masukkan blacklist
				printBlacklisted(gig, revivedCountForGig)
			} else {
				printRevivedGig(gig, revivedCountForGig)
				if err := sendTelegramNotificationRevived(gig, revivedCountForGig); err != nil {
					log.Printf("❌ Gagal kirim Telegram (revived): %v\n", err)
				}
			}
		} else {
			// Gig masih aktif seperti biasa
			if err := store.SaveGig(gig, true, stored.RevivedCount, false); err != nil {
				log.Printf("❌ Gagal update gig aktif: %v\n", err)
				continue
			}
		}
	}

	if err := store.ReplaceActiveGigs(currentIDs); err != nil {
		log.Printf("❌ Gagal update daftar gig aktif: %v\n", err)
	}

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

	total, blacklisted, err := store.Counts()
	if err != nil {
		log.Printf("❌ Gagal hitung database: %v\n", err)
		return
	}

	fmt.Printf("📊 Total gig di database: %d | Blacklist: %d\n", total, blacklisted)
}

func printRevivedGig(g Gig, count int) {
	fmt.Println("─────────────────────────────────────────")
	fmt.Printf("♻️  GIG BANGKIT KEMBALI (ke-%d) — threshold: %d\n", count, revivedThreshold)
	fmt.Printf("   ID       : %s\n", g.ID)
	fmt.Printf("   Judul    : %s\n", g.Title)
	fmt.Printf("   Poster   : %s\n", g.Poster.FullName)
	fmt.Printf("   Budget   : %s %d\n", g.Currency, g.BudgetAmount)
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
	fmt.Printf("   Dibuat   : %s\n", g.CreatedAt.Local().Format("02 Jan 2006 15:04:05"))
	fmt.Println("─────────────────────────────────────────")
}

func sendTelegramMessages(text string) error {
	if telegramBotToken == "" {
		return nil
	}
	var errs []string
	for _, chatID := range telegramChatIDs {
		if err := sendTelegramMessage(chatID, text); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", chatID, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("gagal kirim ke: %s", strings.Join(errs, "; "))
	}
	return nil
}

func sendTelegramMessage(chatID string, text string) error {
	apiEndpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", telegramBotToken)
	values := url.Values{}
	values.Set("chat_id", chatID)
	values.Set("text", text)
	values.Set("parse_mode", "HTML")

	resp, err := http.PostForm(apiEndpoint, values)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%d %s", resp.StatusCode, string(body))
	}

	return nil
}

func sendTelegramNotification(g Gig) error {
	text := fmt.Sprintf("🆕 <b>GIG BARU!</b>\n<b>%s</b>\n\n%s\n%s %d\n%s",
		g.Title, g.Description, g.Currency, g.BudgetAmount, gigLink(g.ID),
	)
	return sendTelegramMessages(text)
}

func sendTelegramNotificationRevived(g Gig, count int) error {
	text := fmt.Sprintf("♻️ <b>GIG TERSEDIA LAGI!</b> (ke-%d, batas: %d)\n<b>%s</b>\n\n%s\n%s %d\n%s",
		count, revivedThreshold,
		g.Title, g.Description, g.Currency, g.BudgetAmount, gigLink(g.ID),
	)
	return sendTelegramMessages(text)
}

func main() {
	telegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	if telegramBotToken == "" {
		log.Println("⚠️  TELEGRAM_BOT_TOKEN tidak diset. Notifikasi Telegram akan dinonaktifkan.")
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "sidegigx.db"
	}

	store, err := NewStore(dbPath)
	if err != nil {
		log.Fatalf("❌ Gagal setup database: %v", err)
	}
	defer store.Close()

	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║   SideGigX Monitor - Mulai Jalan!    ║")
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Printf("🔗 Endpoint  : %s\n", apiURL)
	fmt.Printf("⏱️ Interval  : setiap %s\n", pollInterval)
	fmt.Printf("💾 Database  : %s\n", dbPath)
	fmt.Printf("🚫 Threshold : blacklist setelah bangkit %dx\n\n", revivedThreshold)

	checkForNewGigs(store)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for range ticker.C {
		checkForNewGigs(store)
	}
}
