# Architecture

The application has one media generation path: song based videos built by `internal/mixtape/generator.go`.

- `Best Of` selects tracks by artist and creates a multi segment compilation.
- `Wanna know this song?` creates a single track teaser.
- `internal/audio/indexer.go` maintains and downloads the S3 song library.
- `internal/scheduler/service.go` loads engagement settings and initializes uploaders.
- `internal/bot/telegram.go` schedules both publishers and exposes their settings.

S3 stores `songs.json`, `songs/`, generated collection videos, and `engagement_config.json`.
