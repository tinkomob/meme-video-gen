# Architecture

The application is implemented entirely in Rust. `Cargo.lock` pins dependencies.

- `src/main.rs` loads configuration, starts independent HTTP, Telegram, scheduling,
  indexing, and resource-monitoring tasks, and handles graceful shutdown.
- `src/config.rs` keeps the existing environment variables and aliases.
- `src/storage.rs` provides S3 Signature V4 requests, paginated listings, JSON
  persistence, streaming downloads, and inclusive byte ranges.
- `src/audio.rs` indexes YouTube playlists with yt-dlp, validates audio with
  FFprobe, hashes downloads, and saves each successful song in S3.
- `src/snippet.rs` selects the excerpt, validates/caches covers, serializes
  rendering, streams raw frames to FFmpeg, overlays metadata, and manages temps.
- `src/visualizer.rs`, `visualizer_scenes.rs`, and `visualizer_palettes.rs` implement
  64-band audio analysis and the original ten visual families × three variants.
- `src/schedule.rs` validates/generates/migrates two daily Tomsk slots with 10–14
  hour gaps. `src/service.rs` persists schedule claims before scheduled uploads.
- `src/model.rs` preserves the Go JSON schema. `src/service.rs` saves drafts and
  records each destination as `sending` before upload, then `sent` or `failed`.
  `sending` and `sent` are never automatically retried.
- `src/bot.rs` handles the supported commands, search, previews and draft controls,
  checks the original author/chat, and serializes actions per draft.
- `src/upload.rs` implements silent Telegram uploads and resumable YouTube uploads,
  including OAuth refresh and token persistence. The existing Instagram label
  still routes to the Telegram posts channel.
- `src/friends.rs` matches S3 episode files, filters later story parts from random
  selection, and streams HTTP byte ranges. `assets/friends.html` preserves the
  existing player, recent history, saved position, and part-one continuation.
- `src/bin/song-snippet.rs` and `src/bin/generate-token.rs` replace both Go utilities.

Tests cover pure rules, a mock S3/Telegram HTTP flow with injected write failures,
a full FFmpeg render, and a separately invoked read-only live S3 smoke check.
