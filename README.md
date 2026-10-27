# Song video generator

Rust Telegram bot that generates 20-second music snippets with 75 audio-reactive
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

Twenty-five families each have three geometry/palette variants (0, 1, 2): `silver`,
`violet`, `amber`, `aurora`, `prism`, `galaxy`, `tunnel`, `orbit`, `wave`, `rain`,
`chrome`, `silk`, `analog`, `cover`, `nebula`, `liquid`, `bloom`, `ferro`, `cymatics`, `topography`, `glass`, `ink`, `kinetic`, `lightning`, `interference`.
`random` selects a family and variant. Motion uses 64 logarithmic audio bands;
the original scenes use layered particles and the new scenes use procedural 3D space.
Validated YouTube JPEG covers are cached in S3; a waveform is the fallback.

The player keeps a fixed cover, title and artist layout. All 75 variants share a
spatial flow field driven by twelve independent frequency voices. Each voice has
its own attack, release and delay; responses are precomputed from the audio so
seeking and repeated frames remain deterministic. Music advances local flow clocks
and slowly opens geometry. Accents are calibrated per frequency voice from the clip itself, preserving small
changes in compressed tracks. They accelerate flow up to four times and create
travelling surface highlights without global position kicks. Spatial lights
preserve local peaks instead of averaging them away. Damped spring oscillations are reserved for strings,
ribbons and waves; liquid bodies drift, stretch and merge with moving reflections.

Colours evolve independently across regions and depth, with saturated contrasting
accents selected independently of the cover. Some snippets have no neon; others
have moving, fading local highlights. Background veils, refracted light and
particles occupy different depth planes. `nebula`, `liquid` and `bloom` integrate
procedural density through depth with internal folds, surface lighting and
occlusion; liquid bodies have independent motion and audio responses.

Backgrounds use coordinated coloured gradients with three brightness levels;
the title, artist and progress area has a soft dark scrim for readability.
`ferro` traverses a continuous reflective height field, `cymatics` explores nodal
surfaces, `topography` winds through a stratified cavern, and `glass` looks through
crystal shells. `ink` integrates curled density through space, `kinetic` moves
through rounded sculptural arches, `lightning` follows continuous filaments with
branches in depth, and `interference` passes through folded translucent membranes.
These are procedural approximations, without physical fluid or optical simulation.
The eight new families trace camera rays through world coordinates rather than
repeating screen-space copies. Camera travel continues through procedural geometry;
surface normals, occlusion, distance haze and transmission establish depth. Camera
speed stays independent of musical attacks. Smoothed frequency energy changes
surface colour, emission and the background wash; geometry retains only a small
three-percent audio response. Soft pale cores and two bloom radii give the
surfaces an emissive neon appearance.
Contours use four rotated subpixel samples where neighbouring rays disagree;
smaller ray steps preserve thin surfaces. Rough coloured highlights, depth-filtered
detail, mild optical diffusion and two bloom radii soften the geometric appearance.
Linear light retains headroom through resizing before the shared exposure pass.
Ray samples are split across up to four CPU workers and remain deterministic.
To render a local review (75 stills and eight six-second videos) without S3 or
publication, use `cargo run --release --example visual-preview -- OUTPUT AUDIO COVER`.
Append `--stills` to review composition before encoding videos.
Use `--seconds 3` for shorter motion reviews (1–20 seconds; default 6).
The output directory must be new; the audio must contain at least six seconds.
The review compares the new scenes with the same seeded render in silence and
saves the changed-pixel measurements in `audio-response.json`.

All families use separate low-, mid- and high-frequency attacks measured before
spectrum compression. Bass launches liquid surface waves and light fronts through
galaxy arms and tunnel depth; midrange attacks pluck amber threads, fold silk,
open bloom petals, and sweep highlights across violet, prism and chrome. Aurora
and rain shimmer on high-frequency attacks; wave receives travelling swells and
foam, analog a travelling deflection, and cover a soft radial lighting pulse.
Each frequency region retains its latest wave.

Chrome uses two asymmetric, folded metal ribbons with restrained colour and broad
studio reflections. Their placement leaves room for the cover; musical accents travel through
the reflections instead of inflating a solid glowing shape.

Before rendering, a clip-wide direction pass measures sustained energy and
spectral changes, locates its strongest energetic phrase, and finds rises and
pauses. Smooth envelopes gather the composition before a rise, open it around
the strongest phrase, change colour around spectral turns, and leave more empty
space in pauses. Constant sound does not invent a climax or structural changes;
this analysis does not identify instruments, lyrics or verse/chorus labels.
All decisions are precomputed and deterministic when seeking or replaying.

Depth planes have distinct sharpness and lighting. Defocused elements retain
soft light even without neon. Nebula, liquid, bloom and chrome attenuate distant
particles and their glow behind the material, with near particles in front.

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
