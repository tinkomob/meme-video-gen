# Project instructions

## Build and run

```powershell
cargo build --release --locked --bins
.\target\release\meme-bot.exe
```

Requires Rust 1.99+, FFmpeg/FFprobe, yt-dlp with EJS and Deno, a `.env` file, Telegram credentials,
and S3 credentials. YouTube publishing additionally requires OAuth credentials.

## Architecture

This Rust Telegram bot generates and publishes 20-second song snippets:

- `src/snippet.rs` and `src/visualizer*.rs` render 30 audio-reactive visual designs.
- `src/audio.rs` maintains the S3 song library with hourly playlist indexing.
- `src/service.rs` configures storage and publication and persists drafts/claims.
- `src/schedule.rs` saves two daily times 10–14 hours apart in Asia/Tomsk.
- `src/bot.rs` implements commands and author-only draft publication controls.
- `src/friends.rs` and `assets/` serve the Friends player and video byte ranges.

Preserve existing S3 keys and JSON schemas. Snippet replies/publications are silent.
Run `cargo test --all-targets`, `cargo clippy --all-targets -- -D warnings`, and
`cargo fmt --all -- --check`. Tests require FFmpeg/FFprobe. Live tests are opt-in,
read-only, and never publish. Do not add local Go caches if comparing old code.
