# Song video generator

Rust Telegram bot that generates 20-second music snippets with 45 audio-reactive
visual designs, publishes twice a day, and streams a Friends episode player.

## Requirements and build

Install Rust 1.99+, FFmpeg/FFprobe, and yt-dlp with its default dependencies
(`python -m pip install "yt-dlp[default]"`) for hourly playlist indexing.
YouTube downloading also needs Deno 2.3+ on PATH; see the
[official yt-dlp runtime setup](https://github.com/yt-dlp/yt-dlp/wiki/EJS).
Copy `.env.example` to `.env` and configure Telegram and S3 credentials.
YouTube publishing additionally requires OAuth `client_secrets.json` and
`token.json` in S3, at the bucket root, `tokens/`, or `bot-uploads/`.

```powershell
cargo build --release --locked --bins
.\target\release\meme-bot.exe
```

On Linux run `./target/release/meme-bot`. The HTTP player listens at
[http://localhost:8080/friends](http://localhost:8080/friends).
Set `CARGO_TARGET_DIR` to an external directory if desired. Credentials, generated
media, and build artifacts must stay out of Git.

```powershell
cargo test --locked --all-targets
cargo clippy --locked --all-targets -- -D warnings
cargo fmt --all -- --check
```

The integration suite uses a local mock S3/Telegram server and runs FFmpeg to
verify a complete 20-second, 1080×1920, 30 fps H.264/AAC video. FFmpeg/FFprobe must
be on PATH. It also checks byte-range streaming, persisted publication states,
failed storage writes, and compatibility with the old Go JSON data.

A separate **read-only** live check loads `.env`, reads the existing S3 library,
downloads one song, and streams a 1 KiB Friends video range. It never publishes:

```powershell
cargo test --locked --test live -- --ignored --nocapture
```

## Commands and previews

- `/start`, `/help`, `/chatid` — introduction, supported commands, and chat ID.
- `/song [query]` — browse, search, or download a track.
- `/snippet [style] [variant]` — silently generate a preview with author-only
  controls for publication, regeneration, song changes, and deletion.

```powershell
cargo run --release --bin song-snippet -- --style silver --variant 2 --output champagne-rain.mp4
cargo run --release --bin song-snippet -- --style aurora --song 5XwnM_G3XQ0 --output aurora.mp4
cargo run --release --bin generate-token -- --credentials client_secrets.json --token token.json
```

Rust CLI flags use two hyphens, for example `--output` (formerly `-output`).
The output must not already exist. Both the song-snippet CLI and the service use
the same renderer. Audio must be at least 20 seconds long. Generation is limited
to one renderer at a time; temporary files are cleaned up after use.

Fifteen families each have three geometry/palette variants (0, 1, 2): `silver`,
`violet`, `amber`, `aurora`, `prism`, `galaxy`, `tunnel`, `orbit`, `wave`, `rain`,
`chrome`, `collage`, `silk`, `analog`, `cover`.
`random` selects a family and variant. Motion uses 64 logarithmic audio bands,
three depth planes, particles, bloom, and irregular, independently randomized
camera keyframes.
Validated YouTube JPEG covers are cached in S3; a waveform is the fallback.

Visuals independently randomize four cover/text compositions and 4-8 motion states
with irregular timing. Bass, treble and transient accents drive different movements;
there is no fixed build-up or climax. Cover-derived colours may be blended with
other palettes or skipped entirely. New scenes include reflective liquid chrome,
restrained paper fragments, flowing light ribbons, phosphor traces and softly
animated, blurred cover backdrops.

## Stored state and publishing

Existing Go S3 data is compatible: `songs.json`, `songs/`, `covers/`, `config.json`,
`snippet_schedule.json`, and `snippet-drafts/<id>.{json,mp4}` keep their keys and
JSON formats. Null Go draft selections/maps are accepted. Playlist configuration
is `payload/music_playlists.json`; optional cookies are
`payload/youtube_cookies.txt`. Local playlist JSON remains a fallback.

Two random daily slots are persisted in Asia/Tomsk (UTC+7), 10–14 hours apart,
including midnight. Legacy three-slot schedules migrate without repeating
processed slots. Missed times are skipped and claims are saved before publishing.
Scheduled uploads are silent and send no separate Telegram status messages.

The UI's **Instagram** destination retains the existing behavior: it sends to the
Telegram posts channel configured by `POSTS_CHAT_ID` (or the legacy `POSTS_CHATID`
alias), falling back to S3 `config.json`. YouTube uses its actual upload API.
Manual publication sends the saved video, and `sending`/`sent` destination states
prevent automatic retries after a crash. Check the destination manually if an
upload remains `sending`; the outcome may be uncertain.

Hourly playlist indexing uses yt-dlp and saves the index after each downloaded
song. Telegram retries startup five times; HTTP and scheduling continue if it is
unreachable. Resource monitoring warns at 600 MiB of process memory / 500 Tokio
tasks and shuts down at 1200 MiB / 1000 tasks. Rust process memory replaces Go heap
measurement. SIGINT/SIGTERM trigger graceful shutdown. Logs append to `errors.log`.

## Docker

```sh
docker compose up --build -d
```

The runtime includes FFmpeg, current yt-dlp/EJS, Deno, fonts and CA certificates, runs as a non-root
user, and exposes port 8080. Compose binds that port to localhost.
