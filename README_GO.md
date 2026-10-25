# Song video generator

Telegram bot that generates 20-second music snippets with audio-reactive visuals
and publishes them twice a day. Source audio and song covers are stored in S3.

## Build

Requires Go 1.24+, FFmpeg, Telegram and S3 credentials, and a Gemini/Google API key.

```powershell
go build -o meme-bot.exe ./cmd
.\meme-bot.exe
```

## Bot commands

- `/song [query]` — find and download a track.
- `/snippet [style] [variant]` — generate a 20-second preview and send it silently to the requesting chat; e.g. `/snippet galaxy 2`. With no arguments both are random.
- `/songs` indexing runs hourly.

Song audio and index are stored under `songs/` and `songs.json`.
The Telegram command menu is refreshed at startup with the supported commands.

## Song snippets

Generate a local 20-second, 1080×1920, 30 fps MP4 from a random real song in S3:

```powershell
go run ./cmd/song-snippet -output song-snippet.mp4
```

Run from the project root with `.env` configured and FFmpeg/FFprobe on PATH.
Each render randomly chooses an audio excerpt, one of ten families, and one of
three geometry/palette variants: **30 designs** in total. The first three families
extend the references in `design/song-snippet/`; the others add new scenes.

| Family | Variant 0 | Variant 1 | Variant 2 |
| --- | --- | --- | --- |
| `silver` | Pearl Coast: silver spectrum | Ice Mirror: mirrored blue spectrum | Champagne Rain: falling gold light |
| `violet` | Violet Orbit | Twin Orbit: intertwined pink orbits | Lotus Halo: blue petals |
| `amber` | Amber Silk: diagonal ribbons | Rose Helix: twisting ribbons | Golden Veil: broad flowing bands |
| `aurora` | Borealis: green/cyan curtains | Magenta Curtain | Glacial Lights: icy diagonal curtains |
| `prism` | Neon Crystal: hexagonal facets | Rose Triangle: rotating triangular frames | Ice Diamond: diamond facets |
| `galaxy` | Spiral Nebula: two violet arms | Golden Dust: three warm arms | Comet Field: four icy arms and trails |
| `tunnel` | Neon Dive: circular light tunnel | Sunset Portal: rounded square tunnel | Emerald Vortex: five-lobed tunnel |
| `orbit` | Celestial Globe: intersecting elliptical rings | Solar Weave: wavy gold orbits | Jade Shell: rippling green sphere |
| `wave` | Ocean Grid: perspective wave field | Rose Terrain: crossed wave crests | Mint Tide: standing waves |
| `rain` | Meteor Shower: blue falling trails | Golden Drizzle: diagonal sparks | Emerald Rise: rising curved trails |

```powershell
go run ./cmd/song-snippet -style silver -variant 2 -output champagne-rain.mp4
go run ./cmd/song-snippet -style aurora -song 5XwnM_G3XQ0 -output aurora.mp4
go run ./cmd/song-snippet -style galaxy -variant 2 -output comet-field.mp4
```

The default `-style random` selects from all ten families; `-variant -1` (the
default) randomly chooses a variant. Use `-variant 0`, `1`, or `2` to select its
geometry and coordinated palette. `-song` optionally selects a YouTube ID already
present in the S3 song index. Every render also varies speed, phase, proportions
and particles using a new seed. The player accent follows the chosen palette.
All families render three geometry planes with separate scale, parallax, motion
and focus, plus atmospheric light and foreground particles. Five seeded keyframes
smoothly evolve camera position, geometry, density and palette across the full
20-second snippet. The evolution is reproducible for a seed and stays synchronized
to snippet progress independently of the randomized animation speed.
Backgrounds are generated procedurally from 64 logarithmic
frequency bands and the level of the actual excerpt, not from static mockups.
Rendering uses Go's standard library and FFmpeg; no extra application dependencies.

The larger rounded cover, title, artist and full-song player follow the references.
The progress line and elapsed/total time show the excerpt's position within the
full song. YouTube covers are tried at maxres, SD and HQ resolution, validated,
and cached in S3 at `covers/<video-id>.jpg`. HTTPS is preferred; public-image HTTP
is a fallback for networks blocking YouTube TLS. Each request has a four-second
timeout. If all sources fail, a waveform cover is generated from the song.
Audio must be at least 10 seconds long. Existing output files are never overwritten.
The CLI generates a local preview only. The Telegram `/snippet` command picks a
random library song and returns a silent video with inline controls to the author:

- Toggle Telegram and YouTube, then send to the selected platforms, or send to both.
- Generate a new visual for the same song, choose a different random song, or pick
  a specific song from the paginated library.
- Delete the draft and preview message.

Publication always uploads the saved video, without regenerating it. Draft videos
and metadata are stored in S3 under `snippet-drafts/`, so controls survive restarts.
Only the author can operate them in the original chat. Successful and in-flight
destinations are recorded to prevent repeated clicks from duplicating uploads.
If a process stops during an upload, check the destination; an uncertain in-flight
upload is not automatically repeated. Delete drafts with their inline button when
they are no longer needed. Deleting a draft does not remove already published posts.
Telegram publication targets the configured posts channel; YouTube uses the
connected account. No external publication happens until a send button is pressed.
Docker includes the `song-snippet` binary and DejaVu fonts.

### Automatic snippets

The application generates **two random publication times each day in Asia/Tomsk
(UTC+7)**. One falls in each twelve-hour half of the day; adjacent planned times
are **10–14 hours apart**, including the gap between yesterday's last and today's
first publication. A fresh plan is generated after midnight.

The daily plan and handled slots are saved in S3 as `snippet_schedule.json`.
Restarting the bot preserves the chosen times and prevents replaying a claimed
slot. Slots missed while offline are skipped; they are never published in a burst.
Older three-slot daily plans are migrated to two slots, respecting the number
already handled that day so no extra publication is introduced during the change.
If the bot was offline for multiple days, it creates a plan for the current day.
The scheduler checks once per minute. A slot is saved as handled before generation;
failed or partially uploaded slots are not automatically retried.

The job runs independently of Telegram polling and skips overlap if a previous
scheduled publication is still running. Generation is limited to 10 minutes;
generation plus publication is limited to 15 minutes.

Publication destinations are Telegram and YouTube. Telegram
videos always have `disable_notification=true`; scheduled jobs send no additional
status or error messages to Telegram. Errors and individual upload results are
written to the application logs. The manual command and its replies are silent too.

The Instagram-labelled destination sends to `POSTS_CHAT_ID` (legacy `POSTS_CHATID`
also works). An explicit environment value takes priority; `config.json` in S3
is only a fallback when no destination is configured. Negative channel IDs are supported.
Manual publication shows a silent “Отправляю…” message and edits that same message
to display the Instagram/YouTube results when the upload finishes.
Rebuild and restart the running application to activate the new command and job.

## Repository hygiene

Keep generated previews in `output/`, compiled binaries in `bin/` or the documented
root executable, and Go caches outside the project. These artifacts, logs,
downloaded media and local credentials are excluded by `.gitignore` and
`.dockerignore`. The three reference images in `design/song-snippet/` and embedded
assets in `internal/web/` are source materials and remain versioned.
