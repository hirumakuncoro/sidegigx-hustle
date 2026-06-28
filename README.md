# SideGigX Monitor

Simple Go monitor untuk cek gig baru dari SideGigX dan kirim notifikasi Telegram.

## Build dan Jalankan dengan Docker

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
docker run --rm -e TELEGRAM_BOT_TOKEN="$TELEGRAM_BOT_TOKEN" go-cek-gigs
```

## Catatan

- Container ini tidak membutuhkan port khusus karena aplikasi hanya melakukan polling API dan menulis log ke stdout.
- Pastikan token bot Telegram valid agar notifikasi Telegram bekerja.
