# Meme Video Generator — Web API

## Endpoints

- POST /generate
  - body: { pinterest_urls?: string[], music_playlists?: string[], pin_num?: number, audio_duration?: number }
  - returns: { video_path, thumbnail_path, source_url }

- POST /deploy
  - body: { video_path: string, thumbnail_path: string, source_url: string, audio_path?: string, privacy?: 'public'|'unlisted'|'private' }
  - returns: { status: 'ok', links: { youtube?: string, instagram?: string } }

## Run (dev, Windows PowerShell)

```powershell
python -m venv .venv
. .venv\Scripts\Activate.ps1
pip install -r requirements.txt
uvicorn app.server:app --reload --host 0.0.0.0 --port 8000
```

## Notes
- Requires ffmpeg available in PATH for moviepy and yt-dlp postprocessing.
- Put Pinterest URLs in pinterest_urls.json and music playlists in music_playlists.json or pass in request.
- For uploads set env: INSTAGRAM_USERNAME, INSTAGRAM_PASSWORD, YOUTUBE_API_KEY and have client_secrets.json for YouTube OAuth.
