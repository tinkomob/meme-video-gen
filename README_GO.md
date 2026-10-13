# Song video generator

Telegram bot that creates and publishes Best Of music collections and the daily “Wanna know this song?” teaser. Generated videos and source audio are stored in S3.

## Build

Requires Go 1.24+, FFmpeg, Telegram and S3 credentials, and a Gemini/Google API key.

```powershell
go build -o meme-bot.exe ./cmd
.\meme-bot.exe
```

## Bot commands

- `/setmixtapes bestof` — view/configure Best Of artists and cadence.
- `/setmixtapes teaser` — view/configure the daily teaser time.
- `/song [query]` — find and download a track.
- `/songs` indexing runs hourly.

Best Of and teaser schedules are stored in `engagement_config.json` in S3. Song audio and index are stored under `songs/` and `songs.json`.
