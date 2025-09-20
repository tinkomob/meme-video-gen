import os
import sys

sys.dont_write_bytecode = True
os.environ['PYTHONDONTWRITEBYTECODE'] = '1'

from fastapi import FastAPI, HTTPException, Body
from pydantic import BaseModel
from typing import List, Optional
from .service import generate_meme_video, deploy_to_socials
from .utils import load_urls_json

app = FastAPI(title='Meme Video Generator')

class GenerateRequest(BaseModel):
    pinterest_urls: Optional[List[str]] = None
    music_playlists: Optional[List[str]] = None
    pin_num: Optional[int] = 30
    audio_duration: Optional[int] = 10

class GenerateResponse(BaseModel):
    video_path: Optional[str]
    thumbnail_path: Optional[str]
    source_url: Optional[str]

class DeployRequest(BaseModel):
    video_path: str
    thumbnail_path: str
    source_url: str
    audio_path: Optional[str] = None
    privacy: str = 'public'

@app.post('/generate', response_model=GenerateResponse)
def generate_post(req: Optional[GenerateRequest] = Body(None)):
    pins = load_urls_json('pinterest_urls.json') if (req is None or not req.pinterest_urls) else req.pinterest_urls
    music = load_urls_json('music_playlists.json') if (req is None or not req.music_playlists) else req.music_playlists
    print(f"POST /generate - Loaded pins: {len(pins) if pins else 0} URLs", flush=True)
    print(f"POST /generate - Loaded music: {len(music) if music else 0} playlists", flush=True)
    pin_num = 30 if (req is None) else req.pin_num
    audio_duration = 10 if (req is None) else req.audio_duration
    try:
        result = generate_meme_video(pins, music, pin_num=pin_num, audio_duration=audio_duration)
        return GenerateResponse(video_path=result.video_path, thumbnail_path=result.thumbnail_path, source_url=result.source_url)
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.get('/generate', response_model=GenerateResponse)
def generate_get(pin_num: int = 30, audio_duration: int = 10):
    pins = load_urls_json('pinterest_urls.json')
    music = load_urls_json('music_playlists.json')
    print(f"GET /generate - Loaded pins: {len(pins) if pins else 0} URLs", flush=True)
    print(f"GET /generate - Loaded music: {len(music) if music else 0} playlists", flush=True)
    try:
        result = generate_meme_video(pins, music, pin_num=pin_num, audio_duration=audio_duration)
        return GenerateResponse(video_path=result.video_path, thumbnail_path=result.thumbnail_path, source_url=result.source_url)
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.post('/deploy')
def deploy(req: DeployRequest):
    try:
        links = deploy_to_socials(req.video_path, req.thumbnail_path, req.source_url, req.audio_path, privacy=req.privacy)
        return {'status': 'ok', 'links': links}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)