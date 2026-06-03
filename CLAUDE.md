# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go mod tidy
go build -o meme-bot.exe ./cmd
.\meme-bot.exe
```

**Prerequisites:** Go 1.24+, FFmpeg in PATH, `.env` file with required keys (see `.env.example`).

No formal test suite — validate by running the bot and using Telegram commands `/meme`, `/status`, `/runscheduled`.

## Architecture

The system is a Go Telegram bot that auto-generates meme videos and posts them on a daily schedule.

**Entry point:** `cmd/main.go` → `scheduler.BuildService()` → `bot.NewTelegramBot()`

**Core components:**
- `internal/scheduler/service.go` — central coordinator (`scheduler.Service`) wrapping `MemeService` interface; owns cron jobs, S3 client, config, and thread-safe schedule
- `internal/bot/telegram.go` — Telegram bot; `runSchedulePoster()` goroutine checks schedule every 10s and fires `sendScheduledMemes()`
- `internal/video/generator.go` — combines random audio + source image/video into MP4 via moviego/FFmpeg
- `internal/audio/indexer.go` — downloads tracks from YouTube playlists
- `internal/sources/scraper.go` — collects media from Pinterest, Reddit, Twitter, Google Images (colly + SerpAPI)
- `internal/ai/title_gen.go` — Google Gemini titles; `internal/ai/bratuha_video.go` — Bratuha API for `/idea` command
- `internal/s3/client.go` — MinIO-compatible S3 wrapper used for all persistent storage

**S3 layout:**
```
songs.json / sources.json / memes.json / schedule.json   ← JSON indexes
songs/       ← .m4a audio
sources/     ← .jpg/.png/.mp4 scraped media
memes/       ← .mp4 videos + _thumb.jpg thumbnails
```

**Hourly cron maintenance:** ensures freshness of songs/sources/memes (max 50 sources, 10 memes, 16h age limit).

**Daily schedule:** random posting times in 10:00–23:59 window (Asia/Tomsk UTC+7) with ±30min jitter; stored in S3 as `schedule.json`.

## Key Patterns

- `scheduler.Service.GetSchedule()` / `SetSchedule()` are mutex-guarded — always use these, never access `schedule` directly.
- Config loaded from env vars; `POSTS_CHAT_ID` also persisted to S3 `config.json` at runtime.
- Errors in non-critical paths (single meme generation, individual scrapers) are logged and skipped — the bot keeps running.
- Russian comments exist in older code; English preferred for new code.

## Common Extension Points

**Add a bot command:** add a case in `handleCommand()` in `internal/bot/telegram.go`, implement the handler, update `/help` text.

**Add a source scraper:** implement `scrapeXYZ()` in `internal/sources/scraper.go`, call it from `EnsureSources()`, add a JSON config file.

**Add a cron task:** add a job in `BuildService()` using `robfig/cron` format `"0 0 * * * *"`, access components via `s.impl`.

## Required Environment Variables

See `.env.example`. Minimum to run: `TELEGRAM_BOT_TOKEN`, S3 credentials, `GEMINI_API_KEY` (or `GOOGLE_API_KEY`). Uploaders (Instagram, YouTube, X) are optional.
