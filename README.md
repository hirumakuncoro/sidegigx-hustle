# SideGigX Monitor

Simple Go monitor untuk cek gig baru dari SideGigX dan kirim notifikasi Telegram.

## Jalankan dengan Docker Compose

Cara termudah menjalankan aplikasi:

1. Buat file `.env` (atau set environment variable):
```sh
echo "TELEGRAM_BOT_TOKEN=token_anda_disini" > .env
```

2. Jalankan di background:
```sh
docker compose up -d
```

3. Lihat log:
```sh
docker compose logs -f
```

Data status gig disimpan di SQLite pada volume Docker `sidegigx-data`.

## Struktur Database

Aplikasi memakai satu tabel SQLite bernama `gigs`:

| Kolom | Tipe | Fungsi |
| --- | --- | --- |
| `id` | `TEXT PRIMARY KEY` | ID gig dari API SideGigX. |
| `gig_json` | `TEXT` | Payload gig lengkap dalam format JSON. |
| `last_seen_at` | `TEXT` | Waktu terakhir gig terlihat di response API. |
| `active` | `INTEGER` | `1` kalau gig ada di response polling terakhir, `0` kalau tidak. |
| `revived_count` | `INTEGER` | Jumlah berapa kali gig lama muncul lagi setelah sempat hilang. |
| `blacklisted` | `INTEGER` | `1` kalau gig sudah masuk blacklist dan tidak akan dinotifikasi lagi. |

Default file database adalah `sidegigx.db`. Bisa diubah dengan environment variable `DB_PATH`.

## Build Manual dengan Docker

1. Set token Telegram:

```sh
export TELEGRAM_BOT_TOKEN="<your-bot-token>"
```

2. Build image:

```sh
docker build -t go-cek-gigs .
```

3. Jalankan container:

```sh
docker run --rm \
  -e TELEGRAM_BOT_TOKEN="$TELEGRAM_BOT_TOKEN" \
  -e DB_PATH=/data/sidegigx.db \
  -v sidegigx-data:/data \
  go-cek-gigs
```

## Alur Kerja (Logic Flow)

1. **Inisialisasi**: Baca token Telegram, cek gig pertama kali.
2. **Polling API**: Ambil 10 gig terbaru dari API setiap 1 menit.
3. **Pengecekan Status Gig**:
   - **Gig Baru**: Belum pernah terlihat. Simpan ke SQLite, kirim notifikasi Telegram "Gig Baru!".
   - **Gig Aktif**: Masih ada dari cek sebelumnya. Biarkan.
   - **Gig Bangkit (Revived)**: Sempat hilang tapi muncul lagi. Kirim notifikasi "Gig Tersedia Lagi!".
4. **Blacklist Otomatis**: Jika gig hilang-timbul (bangkit) 2 kali, masuk daftar blacklist tanpa notifikasi Telegram, lalu abaikan selamanya.
5. **Ulangi**: Tunggu 1 menit, kembali ke langkah 2.

## Catatan

- Aplikasi melakukan pengecekan (polling) API setiap 1 menit.
- Container ini tidak membutuhkan port khusus karena aplikasi hanya melakukan polling API dan menulis log ke stdout.
- Pastikan token bot Telegram valid agar notifikasi Telegram bekerja.
