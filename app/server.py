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
    video_path: Optional[str] = None
    thumbnail_path: Optional[str] = None
    source_url: Optional[str] = None
    audio_path: Optional[str] = None
    privacy: str = 'public'
    socials: Optional[List[str]] = None  # ['youtube', 'instagram']
    dry_run: bool = False  # New dry run option
    # Generation parameters (if video_path is None)
    pinterest_urls: Optional[List[str]] = None
    music_playlists: Optional[List[str]] = None
    pin_num: Optional[int] = 30
    audio_duration: Optional[int] = 10

class DeployResponse(BaseModel):
    video_path: Optional[str]
    thumbnail_path: Optional[str]
    source_url: Optional[str]
    deployment_links: Optional[dict] = None

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

@app.post('/deploy', response_model=DeployResponse)
def deploy(req: DeployRequest):
    try:
        # If no video_path provided, generate a new video first
        if not req.video_path:
            print("No video_path provided, generating new video...", flush=True)
            pins = load_urls_json('pinterest_urls.json') if not req.pinterest_urls else req.pinterest_urls
            music = load_urls_json('music_playlists.json') if not req.music_playlists else req.music_playlists
            pin_num = req.pin_num or 30
            audio_duration = req.audio_duration or 10
            
            result = generate_meme_video(pins, music, pin_num=pin_num, audio_duration=audio_duration)
            video_path = result.video_path
            thumbnail_path = result.thumbnail_path
            source_url = result.source_url
            audio_path = result.audio_path
            print(f"Video generated: {video_path}", flush=True)
        else:
            video_path = req.video_path
            thumbnail_path = req.thumbnail_path
            source_url = req.source_url
            audio_path = req.audio_path
            print(f"Using existing video: {video_path}", flush=True)
        
        # Deploy to socials
        if video_path and thumbnail_path:
            print(f"Deploying to socials with privacy: {req.privacy}, platforms: {req.socials or ['all']}, dry_run: {req.dry_run}", flush=True)
            deployment_links = deploy_to_socials(
                video_path, 
                thumbnail_path, 
                source_url, 
                audio_path, 
                privacy=req.privacy,
                socials=req.socials,
                dry_run=req.dry_run
            )
            print(f"Deployment successful: {deployment_links}", flush=True)
        else:
            deployment_links = {'error': 'Missing video or thumbnail path for deployment'}
            print("Deployment failed: Missing required paths", flush=True)
        
        return DeployResponse(
            video_path=video_path,
            thumbnail_path=thumbnail_path,
            source_url=source_url,
            deployment_links=deployment_links
        )
    except Exception as e:
        print(f"Deploy error: {e}", flush=True)
        raise HTTPException(status_code=500, detail=str(e))

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)