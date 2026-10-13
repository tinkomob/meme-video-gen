# Project instructions

## Build and run

```powershell
go build -o meme-bot.exe ./cmd
.\meme-bot.exe
```

Go 1.24+, FFmpeg, a `.env` file, Telegram credentials, S3 credentials, and a Gemini or Google API key are required. Optional social uploaders need their own credentials.

## Architecture

This Go Telegram bot generates and publishes two song based videos:

- `internal/mixtape/generator.go` builds Best Of collections and the “Wanna know this song?” teaser.
- `internal/audio/indexer.go` maintains the song library in S3.
- `internal/scheduler/service.go` configures S3, song indexing, engagement settings, and uploaders.
- `internal/bot/telegram.go` runs the Best Of and teaser publishers and their settings commands.

S3 stores songs, generated Best Of/teaser videos, and engagement settings. Do not add local Go build caches to this repository.
