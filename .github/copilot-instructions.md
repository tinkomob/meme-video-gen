# Project guidance

Follow `AGENTS.md` for project instructions and `ARCHITECTURE.md` for the current architecture.

- Go 1.24+, FFmpeg and FFprobe; keep Go caches outside the repository.
- Generate 10-second music snippets using `internal/snippet/` and its six visual families.
- Keep Telegram commands in `internal/bot/` and scheduling/publication in `internal/scheduler/`.
- Persist the three random daily slots and handled state in S3. Planned intervals are 6–10 hours in Asia/Tomsk.
- Telegram posts and command replies for snippets are silent. Scheduled jobs emit no extra Telegram reports.
- Publication destinations are Telegram and YouTube, configured through the uploader manager.
- Keep credentials, generated previews and binaries out of Git and Docker build contexts.
