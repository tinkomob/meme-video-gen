import os
import sys
import json
from datetime import datetime
from pathlib import Path

sys.dont_write_bytecode = True
os.environ['PYTHONDONTWRITEBYTECODE'] = '1'

from fastapi import FastAPI, HTTPException, Body, Request
from fastapi.responses import HTMLResponse, FileResponse
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates
from pydantic import BaseModel
from typing import List, Optional
from .service import generate_meme_video, deploy_to_socials
from .utils import load_urls_json

app = FastAPI(title='Meme Video Generator')

app.mount("/static", StaticFiles(directory="static"), name="static")
templates = Jinja2Templates(directory="templates")

class GenerateRequest(BaseModel):
    pinterest_urls: Optional[List[str]] = None
    music_playlists: Optional[List[str]] = None
    pin_num: Optional[int] = 1000
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
    socials: Optional[List[str]] = None
    dry_run: bool = False
    pinterest_urls: Optional[List[str]] = None
    music_playlists: Optional[List[str]] = None
    pin_num: Optional[int] = 1000
    audio_duration: Optional[int] = 10

class DeployResponse(BaseModel):
    video_path: Optional[str]
    thumbnail_path: Optional[str]
    source_url: Optional[str]
    deployment_links: Optional[dict] = None

class HistoryItem(BaseModel):
    id: str
    title: Optional[str]
    video_path: Optional[str]
    thumbnail_path: Optional[str]
    source_url: Optional[str]
    deployment_links: Optional[dict]
    created_at: str

HISTORY_FILE = "video_history.json"

def load_history():
    try:
        if os.path.exists(HISTORY_FILE):
            with open(HISTORY_FILE, 'r', encoding='utf-8') as f:
                return json.load(f)
        return []
    except Exception as e:
        print(f"Error loading history: {e}")
        return []

def save_to_history(video_data, deployment_links=None):
    history = load_history()
    
    item = {
        "id": str(len(history) + 1),
        "title": f"Мем-видео {datetime.now().strftime('%d.%m.%Y %H:%M')}",
        "video_path": video_data.get('video_path'),
        "thumbnail_path": video_data.get('thumbnail_path'),
        "source_url": video_data.get('source_url'),
        "deployment_links": deployment_links or {},
        "created_at": datetime.now().isoformat()
    }
    
    history.append(item)
    
    try:
        with open(HISTORY_FILE, 'w', encoding='utf-8') as f:
            json.dump(history, f, ensure_ascii=False, indent=2)
    except Exception as e:
        print(f"Error saving history: {e}")
    
    return item

@app.get("/", response_class=HTMLResponse)
async def home(request: Request):
    return templates.TemplateResponse("index.html", {"request": request})

@app.get("/history", response_class=HTMLResponse)
async def history_page(request: Request):
    return templates.TemplateResponse("history.html", {"request": request})

@app.get("/api/history")
async def get_history():
    return load_history()

@app.get("/video/{video_path:path}")
async def serve_video(video_path: str):
    if os.path.exists(video_path):
        return FileResponse(video_path, media_type="video/mp4")
    raise HTTPException(status_code=404, detail="Video not found")

@app.get("/thumbnail/{thumbnail_path:path}")
async def serve_thumbnail(thumbnail_path: str):
    if os.path.exists(thumbnail_path):
        return FileResponse(thumbnail_path, media_type="image/jpeg")
    raise HTTPException(status_code=404, detail="Thumbnail not found")

@app.post('/generate', response_model=GenerateResponse)
def generate_post(req: Optional[GenerateRequest] = Body(None)):
    pins = load_urls_json('pinterest_urls.json') if (req is None or not req.pinterest_urls) else req.pinterest_urls
    music = load_urls_json('music_playlists.json') if (req is None or not req.music_playlists) else req.music_playlists
    print(f"POST /generate - Loaded pins: {len(pins) if pins else 0} URLs", flush=True)
    print(f"POST /generate - Loaded music: {len(music) if music else 0} playlists", flush=True)
    pin_num = 1000 if (req is None) else req.pin_num
    audio_duration = 10 if (req is None) else req.audio_duration
    try:
        result = generate_meme_video(pins, music, pin_num=pin_num, audio_duration=audio_duration)
        
        response_data = {
            "video_path": result.video_path,
            "thumbnail_path": result.thumbnail_path,
            "source_url": result.source_url
        }
        
        save_to_history(response_data)
        
        return GenerateResponse(**response_data)
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.post('/deploy', response_model=DeployResponse)
def deploy(req: DeployRequest):
    try:
        if not req.video_path:
            print("No video_path provided, generating new video...", flush=True)
            pins = load_urls_json('pinterest_urls.json') if not req.pinterest_urls else req.pinterest_urls
            music = load_urls_json('music_playlists.json') if not req.music_playlists else req.music_playlists
            pin_num = req.pin_num or 1000
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
            
            history = load_history()
            for item in history:
                if item.get('video_path') == video_path:
                    item['deployment_links'] = deployment_links
                    break
            
            try:
                with open(HISTORY_FILE, 'w', encoding='utf-8') as f:
                    json.dump(history, f, ensure_ascii=False, indent=2)
            except Exception as e:
                print(f"Error updating history: {e}")
                
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
        
        return DeployResponse(
            video_path=video_path,
            thumbnail_path=thumbnail_path,
            source_url=source_url,
            deployment_links=deployment_links
        )
    except Exception as e:
        print(f"Deploy error: {e}", flush=True)
        raise HTTPException(status_code=500, detail=str(e))

@app.post("/api/republish/{video_id}")
async def republish_video(video_id: str):
    history = load_history()
    video_item = None
    
    for item in history:
        if item["id"] == video_id:
            video_item = item
            break
    
    if not video_item:
        raise HTTPException(status_code=404, detail="Video not found")
    
    if not video_item.get("video_path") or not os.path.exists(video_item["video_path"]):
        raise HTTPException(status_code=400, detail="Video file not found")
    
    try:
        deployment_links = deploy_to_socials(
            video_item["video_path"],
            video_item["thumbnail_path"],
            video_item["source_url"],
            None,
            privacy='public',
            socials=['youtube', 'instagram', 'tiktok', 'x'],
            dry_run=False
        )
        
        for item in history:
            if item["id"] == video_id:
                item["deployment_links"] = deployment_links
                break
        
        with open(HISTORY_FILE, 'w', encoding='utf-8') as f:
            json.dump(history, f, ensure_ascii=False, indent=2)
        
        return {"success": True, "deployment_links": deployment_links}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.delete("/api/delete/{video_id}")
async def delete_video(video_id: str):
    history = load_history()
    video_item = None
    video_index = None
    
    for i, item in enumerate(history):
        if item["id"] == video_id:
            video_item = item
            video_index = i
            break
    
    if not video_item:
        raise HTTPException(status_code=404, detail="Video not found")
    
    try:
        if video_item.get("video_path") and os.path.exists(video_item["video_path"]):
            os.remove(video_item["video_path"])
        
        if video_item.get("thumbnail_path") and os.path.exists(video_item["thumbnail_path"]):
            os.remove(video_item["thumbnail_path"])
        
        history.pop(video_index)
        
        with open(HISTORY_FILE, 'w', encoding='utf-8') as f:
            json.dump(history, f, ensure_ascii=False, indent=2)
        
        return {"success": True}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

if __name__ == "__main__":
    raise SystemExit("This module is deprecated; use bot.py")