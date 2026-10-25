# Project instructions

## Build and run

```powershell
go build -o meme-bot.exe ./cmd
.\meme-bot.exe
```

Go 1.24+, FFmpeg, a `.env` file, Telegram credentials, S3 credentials, and a Gemini or Google API key are required. Optional social uploaders need their own credentials.

## Architecture

This Go Telegram bot generates and publishes 20-second song snippets:

- `internal/snippet/` renders song snippets with 18 audio-reactive visual designs and YouTube covers.
- `internal/audio/indexer.go` maintains the song library in S3.
- `internal/scheduler/service.go` configures S3, song indexing, and uploaders.
- `internal/scheduler/snippet_schedule.go` saves two random daily times in S3, 10–14 hours apart in Asia/Tomsk.
- `internal/scheduler/snippets.go` publishes snippets silently in Telegram.
- `internal/bot/snippet.go` handles `/snippet [style] [variant]`; `snippet_controls.go` handles author-only publication, song selection and draft controls.

S3 stores songs, covers, manual snippet drafts, schedules and uploader credentials. Do not add local Go build caches to this repository.
