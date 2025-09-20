import os
os.environ['PYTHONDONTWRITEBYTECODE'] = '1'

import random
import shutil
from pathlib import Path
from .config import DEFAULT_PINS_DIR, DEFAULT_AUDIO_DIR, DEFAULT_OUTPUT_VIDEO, DEFAULT_THUMBNAIL
from .utils import ensure_gitignore_entries, load_urls_json
from .sources import scrape_one_from_pinterest
from .audio import download_random_song_from_playlist, extract_random_audio_clip, get_song_title
from .video import convert_to_tiktok_format, generate_thumbnail
from .metadata import generate_metadata_from_source
from .uploaders import youtube_authenticate, youtube_upload_short, instagram_upload

class GenerationResult:
    def __init__(self, video_path: str | None, thumbnail_path: str | None, source_url: str | None, audio_path: str | None):
        self.video_path = video_path
        self.thumbnail_path = thumbnail_path
        self.source_url = source_url
        self.audio_path = audio_path

def generate_meme_video(pinterest_urls: list[str], music_playlists: list[str], pin_num: int = 30, audio_duration: int = 10):
    pins_dir = DEFAULT_PINS_DIR
    audio_dir = DEFAULT_AUDIO_DIR
    Path(pins_dir).mkdir(parents=True, exist_ok=True)
    Path(audio_dir).mkdir(parents=True, exist_ok=True)
    
    # Try Pinterest first
    chosen_pinterest = random.choice(pinterest_urls) if pinterest_urls else None
    downloaded_path = None
    
    if chosen_pinterest:
        print(f"Trying to scrape from Pinterest: {chosen_pinterest}", flush=True)
        downloaded_path = scrape_one_from_pinterest(chosen_pinterest, output_dir=pins_dir, num=pin_num)
        print(f"Pinterest scraping result: {downloaded_path}", flush=True)
    
    # If Pinterest fails, try meme API
    if not downloaded_path:
        print("Pinterest failed, trying meme API...", flush=True)
        from .sources import get_from_meme_api
        meme_url = get_from_meme_api()
        print(f"Meme API result: {meme_url}", flush=True)
        if meme_url:
            try:
                import requests
                headers = {'User-Agent': 'Mozilla/5.0'}
                r = requests.get(meme_url, headers=headers, timeout=10)
                r.raise_for_status()
                ext = '.jpg'
                if 'png' in r.headers.get('content-type', ''):
                    ext = '.png'
                elif 'gif' in r.headers.get('content-type', ''):
                    ext = '.gif'
                downloaded_path = os.path.join(pins_dir, f'meme{ext}')
                with open(downloaded_path, 'wb') as f:
                    f.write(r.content)
                chosen_pinterest = meme_url  # Use meme URL as source
                print(f"Downloaded meme to: {downloaded_path}", flush=True)
                from .utils import add_url_to_history
                add_url_to_history(meme_url)
            except Exception as e:
                print(f"Error downloading meme: {e}", flush=True)
    
    print(f"Final downloaded_path: {downloaded_path}", flush=True)
    if downloaded_path:
        print(f"Downloaded file exists: {os.path.exists(downloaded_path)}", flush=True)
        if os.path.exists(downloaded_path):
            print(f"File size: {os.path.getsize(downloaded_path)} bytes", flush=True)
    
    if not downloaded_path:
        return GenerationResult(None, None, chosen_pinterest, None)
    
    # Rest of the function remains the same...
    audio_clip_path = None
    chosen_music = random.choice(music_playlists) if music_playlists else None
    print(f"Selected music playlist: {chosen_music}", flush=True)
    if chosen_music:
        audio_path = download_random_song_from_playlist(chosen_music, output_dir=audio_dir)
        print(f"Downloaded audio path: {audio_path}", flush=True)
        if audio_path:
            audio_clip_path = extract_random_audio_clip(audio_path, clip_duration=audio_duration)
            print(f"Extracted audio clip path: {audio_clip_path}", flush=True)
            if audio_path != audio_clip_path and os.path.exists(audio_path):
                try:
                    os.remove(audio_path)
                except Exception:
                    pass
    output_path = DEFAULT_OUTPUT_VIDEO
    print(f"Starting video conversion with downloaded_path: {downloaded_path}, output_path: {output_path}", flush=True)
    result_path = convert_to_tiktok_format(downloaded_path, output_path, is_youtube=False, audio_path=audio_clip_path)
    print(f"Video conversion result: {result_path}", flush=True)
    if not result_path or not os.path.exists(result_path):
        print("Video conversion failed", flush=True)
        return GenerationResult(None, None, chosen_pinterest, None)
    
    thumbnail_path = DEFAULT_THUMBNAIL
    print(f"Generating thumbnail for: {output_path}", flush=True)
    thumb_result = generate_thumbnail(output_path, thumbnail_path)
    print(f"Thumbnail generation result: {thumb_result}", flush=True)
    if not thumb_result or not os.path.exists(thumb_result):
        print("Thumbnail generation failed", flush=True)
        return GenerationResult(None, None, chosen_pinterest, None)
    if audio_clip_path and os.path.exists(audio_clip_path):
        try:
            os.remove(audio_clip_path)
        except Exception:
            pass
    if downloaded_path and os.path.exists(downloaded_path):
        try:
            os.remove(downloaded_path)
        except Exception:
            pass
    for d in [pins_dir, audio_dir]:
        try:
            if Path(d).exists():
                shutil.rmtree(d, ignore_errors=True)
        except Exception:
            pass
    ensure_gitignore_entries([f"{pins_dir}/", f"{audio_dir}/"]) 
    return GenerationResult(output_path, thumbnail_path, chosen_pinterest, None)

def deploy_to_socials(video_path: str, thumbnail_path: str, source_url: str, audio_path: str | None, privacy: str = 'public', socials: list[str] | None = None, dry_run: bool = False):
    generated = generate_metadata_from_source(source_url, None, audio_path)
    
    # Default to all socials if none specified
    if socials is None:
        socials = ['youtube', 'instagram']
    
    # Normalize social names to lowercase
    socials = [s.lower() for s in socials]
    
    yt_link = None
    if 'youtube' in socials:
        if dry_run:
            yt_link = f"https://youtu.be/dry-run-{generated['title'].replace(' ', '-').lower()}"
            print(f"DRY RUN: Would upload to YouTube with title: {generated['title']}", flush=True)
        else:
            yt = youtube_authenticate()
            if yt is not None:
                resp = youtube_upload_short(yt, video_path, generated['title'], generated['description'], tags=generated['tags'], privacyStatus=privacy)
                if resp and resp.get('id'):
                    yt_link = f"https://youtu.be/{resp.get('id')}"
    
    insta_link = None
    if 'instagram' in socials:
        if dry_run:
            insta_link = f"https://www.instagram.com/reel/dry-run-{generated['title'].replace(' ', '-').lower()}/"
            print(f"DRY RUN: Would upload to Instagram with caption: {generated.get('description', '')[:50]}...", flush=True)
        else:
            caption = generated.get('description', '').replace('#Shorts', '').strip()
            if audio_path:
                song_title = get_song_title(audio_path)
                if song_title:
                    caption = f"♪ {song_title} ♪\n\n{caption}"
            tags = [t for t in generated.get('tags', []) if t.lower() != 'shorts']
            if tags:
                caption += ('\n\n' + ' '.join(f'#{t}' for t in tags))
            insta = instagram_upload(video_path, caption, thumbnail=thumbnail_path)
            try:
                if insta and getattr(insta, 'code', None):
                    insta_link = f"https://www.instagram.com/reel/{insta.code}/"
            except Exception:
                pass
    
    return {'youtube': yt_link, 'instagram': insta_link}