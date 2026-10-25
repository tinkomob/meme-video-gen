# Architecture

The application generates 20-second song snippets using `internal/snippet/`.

- Six visual families have three geometry/palette variants each. Motion, glow and particles react to the audio's frequency bands and level.
- `internal/audio/indexer.go` maintains the song library in S3.
- `internal/scheduler/service.go` initializes S3, song indexing and uploaders.
- `internal/scheduler/snippet_schedule.go` creates two random daily times in Asia/Tomsk, 10–14 hours apart including midnight, and persists the plan and claimed slots in S3 at `snippet_schedule.json`.
- `internal/scheduler/snippets.go` generates and publishes snippets independently of Telegram polling. Missed slots are skipped, and claimed slots are not repeated after restart.
- `internal/bot/snippet.go` handles `/snippet [style] [variant]`, chooses a random library song by default and silently returns a preview with inline controls.
- `internal/bot/snippet_controls.go` restricts actions to the author in the original chat; `internal/scheduler/snippet_drafts.go` persists manual drafts and destination states under `snippet-drafts/` in S3. Controls select publication destinations, regenerate visuals, change/select songs and delete drafts.
- `cmd/song-snippet` generates local previews from S3 songs.

S3 stores `songs.json`, `songs/`, cached YouTube `covers/`, channel configuration and uploader credentials. Generated snippets use temporary files that are removed after publication. Scheduled Telegram videos are silent, with no separate status messages.
