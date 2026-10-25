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

## Song snippets

Generate a local 10-second, 1080×1920, 30 fps MP4 from a random real song in S3:

```powershell
go run ./cmd/song-snippet -output song-snippet.mp4
```

Run from the project root with `.env` configured and FFmpeg/FFprobe on PATH.
Each render randomly chooses an audio excerpt, one of six families, and one of
three geometry/palette variants: **18 designs** in total. The first three families
extend the references in `design/song-snippet/`; the others add new scenes.

| Family | Variant 0 | Variant 1 | Variant 2 |
| --- | --- | --- | --- |
| `silver` | Pearl Coast: silver spectrum | Ice Mirror: mirrored blue spectrum | Champagne Rain: falling gold light |
| `violet` | Violet Orbit | Twin Orbit: intertwined pink orbits | Lotus Halo: blue petals |
| `amber` | Amber Silk: diagonal ribbons | Rose Helix: twisting ribbons | Golden Veil: broad flowing bands |
| `aurora` | Borealis: green/cyan curtains | Magenta Curtain | Glacial Lights: icy diagonal curtains |
| `prism` | Neon Crystal: hexagonal facets | Rose Triangle: rotating triangular frames | Ice Diamond: diamond facets |
| `galaxy` | Spiral Nebula: two violet arms | Golden Dust: three warm arms | Comet Field: four icy arms and trails |

```powershell
go run ./cmd/song-snippet -style silver -variant 2 -output champagne-rain.mp4
go run ./cmd/song-snippet -style aurora -song 5XwnM_G3XQ0 -output aurora.mp4
go run ./cmd/song-snippet -style galaxy -variant 2 -output comet-field.mp4
```

The default `-style random` selects from all six families; `-variant -1` (the
default) randomly chooses a variant. Use `-variant 0`, `1`, or `2` to select its
geometry and coordinated palette. `-song` optionally selects a YouTube ID already
present in the S3 song index. Every render also varies speed, phase, proportions
and particles using a new seed. The player accent follows the chosen palette.
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
This command generates a preview only; it does not publish or enable a schedule.
Docker includes the `song-snippet` binary and DejaVu fonts.
