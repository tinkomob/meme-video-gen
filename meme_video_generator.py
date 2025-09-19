import requests
import os
import random
import warnings
import argparse
import json
import shutil
from dotenv import load_dotenv
from moviepy import VideoFileClip, ImageClip, ColorClip, CompositeVideoClip, TextClip, vfx, concatenate_videoclips
from moviepy.audio.AudioClip import concatenate_audioclips
from pathlib import Path
from datetime import datetime, timedelta, timezone

try:
    from googleapiclient.discovery import build
    from googleapiclient.http import MediaFileUpload
    from google_auth_oauthlib.flow import InstalledAppFlow
    from google.auth.transport.requests import Request
    import pickle
except Exception:
    build = None

try:
    from instagrapi import Client
except Exception:
    Client = None

# Load environment variables from .env file
load_dotenv()

# Suppress MoviePy warnings about missing frames
warnings.filterwarnings("ignore", message=".*bytes wanted but 0 bytes read.*")

# API Keys from environment variables
YOUTUBE_API_KEY = os.getenv('YOUTUBE_API_KEY')
HISTORY_FILE = 'download_history.json'

def load_history(path: str = HISTORY_FILE):
    try:
        if os.path.exists(path):
            with open(path, 'r', encoding='utf-8') as f:
                data = json.load(f)
                if isinstance(data, dict):
                    data.setdefault('urls', [])
                    return data
    except Exception:
        pass
    return {'urls': []}

def save_history(history: dict, path: str = HISTORY_FILE):
    try:
        with open(path, 'w', encoding='utf-8') as f:
            json.dump(history, f, ensure_ascii=False, indent=2)
    except Exception:
        pass

def add_url_to_history(url: str, path: str = HISTORY_FILE):
    try:
        hist = load_history(path)
        if url and url not in hist['urls']:
            hist['urls'].append(url)
            save_history(hist, path)
    except Exception:
        pass

def get_from_meme_api():
    """Get meme from meme-api.com"""
    url = "https://meme-api.com/gimme"
    try:
        response = requests.get(url)
        response.raise_for_status()
        data = response.json()
        if not data.get('nsfw', True):
            meme_url = data['url']
            hist = load_history()
            if meme_url in hist['urls']:
                return None  # Skip if already downloaded
            return meme_url
    except:
        pass
    return None

# Removed Reddit and Instagram sources

def get_from_youtube():
    """Get a random YouTube Short (requires API key) with varied query and paging"""
    if not YOUTUBE_API_KEY:
        return None
    base_url = "https://www.googleapis.com/youtube/v3/search"
    queries = [
        'meme shorts', 'funny meme shorts', 'hilarious meme short', 'dank meme short',
        'funny fails short', 'prank short', 'cat meme short', 'dog meme short',
        'gaming meme short', 'try not to laugh short', 'viral meme short', 'top memes short'
    ]
    q = random.choice(queries)
    order = random.choice(['date', 'relevance', 'viewCount'])
    regions = ['US', 'GB', 'CA', 'AU', 'IN', 'RU', 'DE', 'FR', 'BR']
    region = random.choice(regions)
    days_back = random.randint(0, 120)
    after_dt = (datetime.now(timezone.utc) - timedelta(days=days_back)).replace(hour=0, minute=0, second=0, microsecond=0)
    published_after = after_dt.isoformat().replace('+00:00', 'Z')
    params = {
        'part': 'snippet',
        'type': 'video',
        'videoDuration': 'short',
        'safeSearch': 'strict',
        'key': YOUTUBE_API_KEY,
        'maxResults': 25,
        'q': q,
        'order': order,
        'regionCode': region,
        'publishedAfter': published_after,
    }
    try:
        items = []
        seen = set(load_history().get('urls', []))
        page_token = None
        pages_to_fetch = random.randint(1, 3)
        for _ in range(pages_to_fetch):
            if page_token:
                params['pageToken'] = page_token
            response = requests.get(base_url, params=params, timeout=15)
            response.raise_for_status()
            data = response.json()
            batch = data.get('items', [])
            if batch:
                items.extend(batch)
            page_token = data.get('nextPageToken')
            if not page_token:
                break
        if not items:
            return None
        # filter out seen URLs
        fresh_items = []
        for it in items:
            vid = (it.get('id') or {}).get('videoId')
            if vid:
                url = f"https://www.youtube.com/watch?v={vid}"
                if url not in seen:
                    fresh_items.append(it)
        pick_pool = fresh_items if fresh_items else items
        choice = random.choice(pick_pool)
        video_id = choice.get('id', {}).get('videoId')
        if not video_id:
            return None
        return f"https://www.youtube.com/watch?v={video_id}"
    except Exception:
        return None

def get_random_meme_url(source='all'):
    """
    Fetches a random meme from specified source or all sources.
    """
    source_map = {
        'meme-api': [get_from_meme_api],
        'youtube': [get_from_youtube],
        'all': [get_from_meme_api, get_from_youtube]
    }
    
    sources = source_map.get(source, source_map['all'])
    
    if source == 'all':
        # Shuffle sources for randomness
        random.shuffle(sources)
    
    for source_func in sources:
        url = source_func()
        if url:
            source_name = source_func.__name__.replace('get_from_', '')
            print(f"Got meme from {source_name}")
            return url
    
    print(f"Failed to get meme from {source} source(s)")
    return None

def download_random_song_from_playlist(playlist_url: str, output_dir: str = 'audio', audio_format: str = 'mp3'):
    """
    Downloads a random song from a YouTube/YouTube Music playlist as audio using yt-dlp Python API.
    Returns the path to the downloaded audio file.
    """
    import yt_dlp

    try:
        Path(output_dir).mkdir(parents=True, exist_ok=True)

        list_opts = {
            'quiet': True,
            'no_warnings': True,
            'skip_download': True,
            'extract_flat': 'in_playlist',
            'ignoreerrors': True,
        }
        with yt_dlp.YoutubeDL(list_opts) as ydl:
            info = ydl.extract_info(playlist_url, download=False)
        entries = (info or {}).get('entries') or []
        ids = []
        for e in entries:
            if not e:
                continue
            vid = e.get('id') or e.get('url')
            if vid and len(vid) >= 6:
                ids.append(vid)
        if not ids:
            print('No videos found in playlist')
            return None
        video_id = random.choice(ids)
        if not video_id.startswith('http'):
            video_url = f'https://www.youtube.com/watch?v={video_id}'
        else:
            video_url = video_id

        ydl_opts = {
            'format': 'bestaudio/best',
            'outtmpl': os.path.join(output_dir, '%(id)s.%(ext)s'),
            'postprocessors': [{
                'key': 'FFmpegExtractAudio',
                'preferredcodec': audio_format,
                'preferredquality': '192',
            }],
            'quiet': True,
            'no_warnings': True,
        }
        with yt_dlp.YoutubeDL(ydl_opts) as ydl:
            ydl.download([video_url])

        expected_path = os.path.join(output_dir, f"{video_id}.{audio_format}")
        if os.path.exists(expected_path):
            print(f'Downloaded audio: {expected_path}')
            return expected_path
        # Fallback: find any mp3 created for this id
        for root, _, files in os.walk(output_dir):
            for f in files:
                if f.startswith(video_id) and f.lower().endswith(f'.{audio_format}'):
                    p = os.path.join(root, f)
                    print(f'Downloaded audio: {p}')
                    return p
        print('Audio download failed')
        return None
    except Exception as e:
        print(f"Error downloading song: {e}")
        return None

def scrape_one_from_pinterest(board_url: str, output_dir: str = 'pins', num: int = 30, min_resolution: tuple | None = None):
    """
    Scrapes media from a Pinterest board/pin URL and downloads them locally using pinterest-dl.
    For search URLs, uses web scraping to get images.
    Returns a single local file path randomly chosen from the downloaded items.
    """
    try:
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        
        # Check if it's a search URL
        if 'search/pins' in board_url:
            return scrape_pinterest_search(board_url, output_dir, num, min_resolution)
        
        # Original board scraping logic
        from pinterest_dl import PinterestDL
        client = PinterestDL.with_api(timeout=3, verbose=False)
        # First scrape the media
        kwargs = {'url': board_url, 'num': num}
        if min_resolution is not None:
            kwargs['min_resolution'] = min_resolution
        scraped_medias = client.scrape(**kwargs)
        if not scraped_medias:
            print('No media scraped from Pinterest URL')
            return None
        # Then download
        downloaded_items = PinterestDL.download_media(
            media=scraped_medias,
            output_dir=output_dir,
            download_streams=True,
        )
        candidates = []
        try:
            for item in downloaded_items or []:
                if isinstance(item, str) and os.path.isfile(item):
                    candidates.append(item)
                elif isinstance(item, dict):
                    p = item.get('path') or item.get('filepath') or item.get('file')
                    if p and os.path.isfile(p):
                        candidates.append(p)
        except Exception:
            pass
        if not candidates:
            try:
                for root, _, files in os.walk(output_dir):
                    for f in files:
                        if f.lower().endswith(('.jpg', '.jpeg', '.png', '.gif', '.mp4', '.webm', '.mov')):
                            candidates.append(os.path.join(root, f))
            except Exception:
                pass
        if not candidates:
            print('No media downloaded from Pinterest URL')
            return None
        choice = random.choice(candidates)
        print(f"Picked local media from Pinterest: {choice}")
        return choice
    except Exception as e:
        import traceback
        print(f"Failed to scrape Pinterest: {e}")
        print(traceback.format_exc())
        return None

def scrape_pinterest_search(search_url: str, output_dir: str = 'pins', num: int = 30, min_resolution: tuple | None = None):
    """
    Scrapes images from Pinterest search results using web scraping.
    Returns a single local file path randomly chosen from the downloaded items.
    """
    try:
        import requests
        from bs4 import BeautifulSoup
        import urllib.parse
        
        headers = {
            'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36'
        }
        
        response = requests.get(search_url, headers=headers, timeout=10)
        response.raise_for_status()
        
        soup = BeautifulSoup(response.text, 'html.parser')
        
        # Find image URLs in the search results
        image_urls = []
        
        # Look for img tags with Pinterest image URLs
        for img in soup.find_all('img'):
            src = img.get('src')
            if src and ('pinimg.com' in src or 'pinterest.com' in src):
                # Convert relative URLs to absolute
                if src.startswith('//'):
                    src = 'https:' + src
                elif src.startswith('/'):
                    src = 'https://www.pinterest.com' + src
                
                # Get higher resolution version if available
                if '236x' in src:
                    src = src.replace('236x', '564x')
                elif '474x' in src:
                    src = src.replace('474x', '736x')
                
                image_urls.append(src)
        
        if not image_urls:
            print('No images found in Pinterest search results. Pinterest search pages load content dynamically with JavaScript.')
            print('Consider using Pinterest board URLs instead, or try other sources like meme-api or YouTube.')
            return None
        
        # Limit to num images
        image_urls = image_urls[:num]
        
        # Download images
        downloaded_files = []
        for i, img_url in enumerate(image_urls):
            try:
                img_response = requests.get(img_url, headers=headers, timeout=10)
                img_response.raise_for_status()
                
                # Determine file extension
                content_type = img_response.headers.get('content-type', '')
                if 'jpeg' in content_type or 'jpg' in content_type:
                    ext = '.jpg'
                elif 'png' in content_type:
                    ext = '.png'
                elif 'gif' in content_type:
                    ext = '.gif'
                else:
                    ext = '.jpg'  # default
                
                filename = f"pinterest_search_{i}{ext}"
                filepath = os.path.join(output_dir, filename)
                
                with open(filepath, 'wb') as f:
                    f.write(img_response.content)
                
                downloaded_files.append(filepath)
                print(f"Downloaded: {filepath}")
                
                # Small delay to be respectful
                import time
                time.sleep(0.5)
                
            except Exception as e:
                print(f"Failed to download {img_url}: {e}")
                continue
        
        if not downloaded_files:
            print('No images downloaded from Pinterest search')
            return None
        
        # Pick a random file
        choice = random.choice(downloaded_files)
        print(f"Picked local media from Pinterest search: {choice}")
        return choice
        
    except Exception as e:
        print(f"Failed to scrape Pinterest search: {e}")
        return None

def extract_random_audio_clip(audio_path: str, clip_duration: int = 10, output_path: str = None):
    """
    Extracts a random clip of specified duration from the audio file.
    Returns the path to the clipped audio file.
    """
    try:
        from moviepy import AudioFileClip
        
        audio_clip = AudioFileClip(audio_path)
        total_duration = audio_clip.duration
        
        if total_duration < clip_duration:
            # If audio is shorter, loop it or just use as is
            if total_duration < clip_duration / 2:
                # Loop to make it longer
                n_loops = int(clip_duration / total_duration) + 1
                clips = [audio_clip] * n_loops
                audio_clip = concatenate_audioclips(clips)
                start_time = 0
                end_time = clip_duration
            else:
                start_time = 0
                end_time = total_duration
        else:
            # Pick random start time
            max_start = total_duration - clip_duration
            start_time = random.uniform(0, max_start)
            end_time = start_time + clip_duration
        
        clipped_audio = audio_clip.subclipped(start_time, end_time)
        
        if output_path is None:
            output_path = audio_path.replace('.mp3', '_clip.mp3').replace('.wav', '_clip.wav')
        
        clipped_audio.write_audiofile(output_path, write_logfile=False, logger=None)
        audio_clip.close()
        clipped_audio.close()
        
        print(f"Extracted audio clip: {output_path} ({clip_duration}s from {start_time:.1f}s to {end_time:.1f}s)")
        return output_path
    except Exception as e:
        print(f"Error extracting audio clip: {e}")
        return None

def apply_random_effects(clip):
    """
    Applies random effects and animations to the video clip.
    """
    effects = [
        lambda c: c.fx(vfx.blackwhite),
        lambda c: c.fx(vfx.mirrorx),
        lambda c: c.fx(vfx.mirrory),
        lambda c: c.fx(vfx.invert_colors),
        lambda c: c.fx(vfx.fadein, 2),
        lambda c: c.fx(vfx.fadeout, 2),
        lambda c: c.fx(vfx.crop, x1=50, x2=300),
        lambda c: c.fx(vfx.colorx, 1.5),  # increase saturation
        lambda c: c.fx(vfx.colorx, 0.5),  # decrease saturation
        # Animated effects
        lambda c: c.fx(vfx.resize, lambda t: 1 + 0.3 * (t / c.duration)),  # zoom in over time
        lambda c: c.fx(vfx.rotate, lambda t: 10 * (t / c.duration)),  # rotate over time
        lambda c: c.fx(vfx.colorx, lambda t: 1 + 0.5 * (t / c.duration)),  # color change over time
    ]
    
    # Randomly select 1-4 effects
    num_effects = random.randint(1, 4)
    selected_effects = random.sample(effects, num_effects)
    
    # Apply effects in sequence
    for effect in selected_effects:
        try:
            clip = effect(clip)
        except (AttributeError, TypeError):
            # Skip effects that don't work on this clip type or with parameters
            continue
    
    return clip

def download_file(url, local_filename):
    """
    Downloads a file from a URL to a local path.
    Handles YouTube URLs with yt-dlp.
    """
    try:
        file_extension = os.path.splitext(url)[1]
        local_filename_with_extension = local_filename + file_extension
        
        if 'youtube.com' in url or 'youtu.be' in url:
            # Use yt-dlp for YouTube videos and get actual filename
            import yt_dlp
            ydl_opts = {
                'outtmpl': local_filename + '.%(ext)s',
                'format': 'best[height<=720]',
                'quiet': True,
                'noprogress': True,
                'no_warnings': True,
            }
            with yt_dlp.YoutubeDL(ydl_opts) as ydl:
                info = ydl.extract_info(url, download=True)
                downloaded = ydl.prepare_filename(info)
                meta = {
                    'title': info.get('title'),
                    'uploader': info.get('uploader'),
                    'webpage_url': info.get('webpage_url') or url,
                    'duration': info.get('duration')
                }
            local_filename_with_extension = downloaded
        else:
            # Regular download for other URLs
            with requests.get(url, stream=True) as r:
                r.raise_for_status()
                with open(local_filename_with_extension, 'wb') as f:
                    for chunk in r.iter_content(chunk_size=8192):
                        f.write(chunk)
        
        # Add URL to history after successful download
        try:
            add_url_to_history(url)
        except Exception:
            pass
        
        # If meta exists (YouTube), return tuple (path, meta)
        try:
            return (local_filename_with_extension, meta)
        except NameError:
            return local_filename_with_extension
    except Exception as e:
        print(f"Error downloading file: {e}")
        return None

def convert_to_tiktok_format(input_path, output_path, is_youtube=False, audio_path=None):
    """
    Converts a GIF or image to a TikTok-formatted MP4 video (9:16).
    For YouTube videos, do not loop or cutoff; keep original duration.
    For GIFs/images, loop to at least 10 seconds.
    Optionally adds audio from audio_path.
    """
    base_clip = None
    clip = None
    concat_clip = None
    background = None
    final_clip = None
    audio_clip = None
    try:
        file_extension = os.path.splitext(input_path)[1].lower()
        if file_extension in ['.png', '.jpg', '.jpeg']:
            clip = ImageClip(input_path, duration=10)
        else:
            base_clip = VideoFileClip(input_path)
            if is_youtube:
                # For YouTube videos, keep original duration, no looping or cutoff
                clip = base_clip
            else:
                # For GIFs or other videos, loop if <10s
                if base_clip.duration < 10:
                    n_loops = int(10 / base_clip.duration) + 1
                    clips = [base_clip] * n_loops
                    concat_clip = concatenate_videoclips(clips)
                    clip = concat_clip.with_duration(10)
                else:
                    clip = base_clip

        # Ensure video is <=60s for Shorts
        if clip.duration > 60:
            clip = clip.with_duration(60)

        tiktok_resolution = (1080, 1920)
        clip_resized = clip.resized(width=tiktok_resolution[0])
        clip_resized = apply_random_effects(clip_resized)

        background = ColorClip(size=tiktok_resolution, color=(0, 0, 0), duration=clip_resized.duration)
        final_clip = CompositeVideoClip([background, clip_resized.with_position("center")])
        
        # Add audio if provided
        if audio_path and os.path.exists(audio_path):
            print(f"Adding audio from {audio_path} to video")
            from moviepy import AudioFileClip
            audio_clip = AudioFileClip(audio_path)
            print(f"Audio duration: {audio_clip.duration}, Video duration: {final_clip.duration}")
            # Trim audio to match video duration
            if audio_clip.duration > final_clip.duration:
                audio_clip = audio_clip.subclipped(0, final_clip.duration)
            elif audio_clip.duration < final_clip.duration:
                # Loop audio if shorter
                n_loops = int(final_clip.duration / audio_clip.duration) + 1
                audio_clips = [audio_clip] * n_loops
                audio_clip = concatenate_audioclips(audio_clips).subclipped(0, final_clip.duration)
            final_clip = final_clip.with_audio(audio_clip)
            print("Audio added to video")
        else:
            print("No audio to add" if not audio_path else f"Audio file does not exist: {audio_path}")
        
        final_clip.write_videofile(output_path, codec="libx264", audio_codec="aac", fps=24)
        print(f"Successfully converted to {output_path}")

    except Exception as e:
        print(f"Error during video conversion: {e}")
    finally:
        # Close clips to release file handles on Windows
        try:
            if final_clip:
                final_clip.close()
        except Exception:
            pass
        try:
            if audio_clip:
                audio_clip.close()
        except Exception:
            pass
        try:
            if background:
                background.close()
        except Exception:
            pass
        try:
            if clip:
                clip.close()
        except Exception:
            pass
        try:
            if concat_clip:
                concat_clip.close()
        except Exception:
            pass
        try:
            if base_clip:
                base_clip.close()
        except Exception:
            pass


def generate_thumbnail(video_path: str, output_path: str, time: float = 1.0):
    """
    Generates a thumbnail image from the video at the specified time.
    """
    try:
        from moviepy import VideoFileClip
        from PIL import Image
        import numpy as np

        with VideoFileClip(video_path) as clip:
            frame = clip.get_frame(time)
            img = Image.fromarray(frame)
            img.save(output_path)
            print(f"Generated thumbnail: {output_path}")
            return output_path
    except Exception as e:
        print(f"Error generating thumbnail: {e}")
        return None


def add_text_to_video(input_path, output_path, text, position=("center", "bottom")):
    """
    Adds text to a video file.
    """
    try:
        with VideoFileClip(input_path) as video_clip:
            with TextClip(text=text, font_size=70, color='white', stroke_color='black', stroke_width=2) as txt_clip:
                txt_clip = txt_clip.with_position(position).with_duration(video_clip.duration)
                final_clip = CompositeVideoClip([video_clip, txt_clip])
                final_clip.write_videofile(output_path, codec="libx264", audio_codec="aac", fps=24)
        print(f"Successfully added text to {output_path}")
        return output_path
    except Exception as e:
        print(f"Error adding text to video: {e}")
        return None


def youtube_authenticate(credentials_path: str = 'client_secrets.json', token_path: str = 'token.pickle'):
    scopes = [
        'https://www.googleapis.com/auth/youtube.upload',
        'https://www.googleapis.com/auth/youtube'
    ]
    creds = None
    if Path(token_path).exists():
        with open(token_path, 'rb') as token:
            creds = pickle.load(token)
    if creds is None or not hasattr(creds, 'valid') or not creds.valid:
        if creds and hasattr(creds, 'expired') and creds.expired and hasattr(creds, 'refresh_token') and creds.refresh_token:
            creds.refresh(Request())
        else:
            flow = InstalledAppFlow.from_client_secrets_file(credentials_path, scopes)
            creds = flow.run_local_server(port=0)
        with open(token_path, 'wb') as token:
            pickle.dump(creds, token)
    return build('youtube', 'v3', credentials=creds)


def generate_metadata_from_source(source_url: str, download_meta: dict | None, audio_path: str = None):
    title = None
    description = ''
    tags = ['meme', 'funny', 'shorts']
    
    # Use song title as video title if audio is available
    if audio_path:
        title = get_song_title(audio_path)
        print(f"Using song title: {title}")
    elif download_meta:
        title = download_meta.get('title')
        # add uploader as tag
        if download_meta.get('uploader'):
            tags.insert(0, download_meta.get('uploader'))
    else:
        # fallback heuristics
        if 'youtube.com' in source_url or 'youtu.be' in source_url:
            title = 'Funny YouTube Short #Shorts'
        else:
            title = 'Funny Meme #Shorts'
    
    # Add random fact to description
    random_fact = get_random_fact()
    description = f"Did you know? {random_fact}\n\n#Shorts #Meme #Funny"
    
    # ensure #Shorts in title
    if title and '#Shorts' not in title:
        title += ' #Shorts'
    
    # sanitize title
    if title and len(title) > 100:
        title = title[:97] + '...'
    
    return {'title': title, 'description': description, 'tags': tags}

def youtube_upload_short(youtube, file_path: str, title: str, description: str = '', tags=None, categoryId='24', privacyStatus='public'):
    if tags is None:
        tags = ['shorts', 'meme', 'funny']
    media = MediaFileUpload(file_path, chunksize=-1, resumable=True, mimetype='video/mp4')
    request = youtube.videos().insert(
        part='snippet,status',
        body={
            'snippet': {
                'title': title,
                'description': description,
                'tags': tags,
                'categoryId': categoryId
            },
            'status': {
                'privacyStatus': privacyStatus,
                'selfDeclaredMadeForKids': False
            }
        },
        media_body=media
    )
    response = None
    while response is None:
        status, response = request.next_chunk()
    return response

def load_urls_json(file_path: str, default_urls: list[str] | None = None):
    try:
        p = Path(file_path)
        if not p.exists():
            if default_urls is None:
                default_urls = []
            with open(p, 'w', encoding='utf-8') as f:
                json.dump(default_urls, f, ensure_ascii=False, indent=2)
            return list(default_urls)
        with open(p, 'r', encoding='utf-8') as f:
            data = json.load(f)
            if isinstance(data, list):
                return [x for x in data if isinstance(x, str) and x.strip()]
    except Exception:
        pass
    return []

def get_song_title(audio_path: str):
    """
    Extracts song title and author from audio file metadata.
    Returns formatted title as "Author - Song Name" or fallback.
    """
    try:
        import yt_dlp
        
        # Extract video ID from filename
        filename = os.path.basename(audio_path)
        video_id = filename.split('.')[0]  # Remove extension
        
        # Get metadata using yt-dlp
        with yt_dlp.YoutubeDL({'quiet': True, 'no_warnings': True}) as ydl:
            info = ydl.extract_info(f'https://www.youtube.com/watch?v={video_id}', download=False)
            
            # Get author/uploader
            author = info.get('uploader', '') or info.get('channel', '') or info.get('artist', '')
            title = info.get('title', '')
            
            if not title:
                return f"Song: {video_id}"
            
            # Clean up the title (remove common YouTube artifacts)
            title = title.replace('[Official Video]', '').replace('[Official Music Video]', '')
            title = title.replace('(Official Video)', '').replace('(Official Music Video)', '')
            title = title.replace('[Audio]', '').replace('(Audio)', '')
            title = title.replace('[Official Audio]', '').replace('(Official Audio)', '')
            title = title.replace('[Lyrics]', '').replace('(Lyrics)', '')
            title = title.strip()
            
            # Try to extract artist and song from title
            # Common patterns: "Artist - Song", "Song by Artist", "Artist | Song"
            song_name = title
            extracted_artist = ""
            
            # Pattern 1: "Artist - Song"
            if ' - ' in title:
                parts = title.split(' - ', 1)
                if len(parts) == 2:
                    extracted_artist = parts[0].strip()
                    song_name = parts[1].strip()
            
            # Pattern 2: "Song by Artist"  
            elif ' by ' in title.lower():
                by_index = title.lower().find(' by ')
                if by_index != -1:
                    song_name = title[:by_index].strip()
                    extracted_artist = title[by_index + 4:].strip()
            
            # Pattern 3: "Artist | Song"
            elif ' | ' in title:
                parts = title.split(' | ', 1)
                if len(parts) == 2:
                    extracted_artist = parts[0].strip()
                    song_name = parts[1].strip()
            
            # Use extracted artist if available, otherwise use uploader
            if extracted_artist:
                author = extracted_artist
            elif not author:
                author = "Unknown Artist"
            
            # Clean up author name
            author = author.replace('[Official]', '').replace('(Official)', '').strip()
            
            # Format as "Author - Song Name"
            formatted_title = f"{author} - {song_name}"
            
            # Ensure it's not too long for YouTube
            if len(formatted_title) > 95:  # Leave room for #Shorts
                # Truncate song name if too long
                max_song_length = 95 - len(author) - 3  # 3 for " - "
                if max_song_length > 10:
                    song_name = song_name[:max_song_length].strip()
                    formatted_title = f"{author} - {song_name}"
                else:
                    # If author is too long, truncate it
                    formatted_title = formatted_title[:95]
            
            return formatted_title
            
    except Exception as e:
        print(f"Could not extract song title: {e}")
    
    # Fallback: use filename
    filename = os.path.basename(audio_path)
    name_without_ext = os.path.splitext(filename)[0]
    return f"Song: {name_without_ext}"

def get_random_fact():
    """
    Fetches a random fact from API Ninjas or returns a fallback fact.
    """
    try:
        # Try to get API key from environment
        api_key = os.getenv('API_NINJAS_KEY')
        
        if api_key:
            import requests
            
            url = "https://api.api-ninjas.com/v1/facts"
            headers = {'X-Api-Key': api_key}
            
            response = requests.get(url, headers=headers, timeout=10)
            response.raise_for_status()
            
            data = response.json()
            if data and isinstance(data, list) and len(data) > 0:
                fact = data[0].get('fact', '')
                if fact:
                    return fact
        
        print("API Ninjas key not found or API call failed, using fallback fact")
        
    except Exception as e:
        print(f"Error fetching random fact: {e}")
    
    # Fallback facts
    fallback_facts = [
        "Did you know? The shortest war in history lasted only 38-45 minutes.",
        "Fun fact: A group of flamingos is called a 'flamboyance'.",
        "Interesting: Octopuses have three hearts and blue blood.",
        "Did you know? The Great Wall of China is visible from space, but only under perfect conditions.",
        "Fun fact: A day on Venus is longer than its year.",
        "Interesting: Bananas are berries, but strawberries aren't.",
        "Did you know? The human brain uses about 20% of the body's total energy.",
        "Fun fact: There are more possible games of chess than atoms in the observable universe.",
        "Interesting: A shrimp's heart is in its head.",
        "Did you know? The first computer programmer was Ada Lovelace in 1842."
    ]
    
    return random.choice(fallback_facts)

def ensure_gitignore_entries(entries: list[str], gitignore_path: str = '.gitignore'):
    try:
        existing = set()
        p = Path(gitignore_path)
        if p.exists():
            with open(p, 'r', encoding='utf-8') as f:
                for line in f:
                    existing.add(line.strip())
        with open(p, 'a', encoding='utf-8') as f:
            for e in entries:
                if e not in existing:
                    f.write(e + '\n')
    except Exception:
        pass

def main():
    """
    Main function to run the process.
    """
    parser = argparse.ArgumentParser(description='Generate meme videos from Pinterest and upload to YouTube Shorts')
    parser.add_argument('--pinterest-url', default='https://www.pinterest.com/thisisaprofilename/out-of-context-pictures/', help='Pinterest board or pin URL to scrape')
    parser.add_argument('--pinterest-json', default='pinterest_urls.json', help='Path to JSON file with array of Pinterest URLs')
    parser.add_argument('--pin-num', type=int, default=30, help='How many items to scrape from Pinterest before picking one')
    parser.add_argument('--pins-dir', default='pins', help='Directory to store downloaded Pinterest media')
    parser.add_argument('--music-playlist-url', default='https://music.youtube.com/playlist?list=OLAK5uy_kPA15vwfzRqBQIY1zMkFujv_WaigtvFDY', help='YouTube Music playlist URL to download random song from')
    parser.add_argument('--music-json', default='music_playlists.json', help='Path to JSON file with array of music playlist URLs')
    parser.add_argument('--audio-duration', type=int, default=10, help='Duration of random audio clip in seconds')
    parser.add_argument('--audio-dir', default='audio', help='Directory to store downloaded audio')
    parser.add_argument('--no-upload', action='store_true', help='Skip uploading the generated video to YouTube Shorts and Instagram Reels')
    parser.add_argument('--instagram-only', action='store_true', help='Upload only to Instagram Reels, skip YouTube Shorts')
    parser.add_argument('--force-shorts', action='store_true', help='Ensure uploaded video is <= 60s by trimming if needed')
    parser.add_argument('--title', default='Funny Meme Short', help='Title for YouTube upload')
    parser.add_argument('--description', default='', help='Description for YouTube upload')
    parser.add_argument('--privacy', default='public', choices=['public','unlisted','private'], help='Privacy status for YouTube upload')
    
    args = parser.parse_args()
    
    print("Fetching meme from Pinterest...")

    output_mp4_path = "tiktok_video.mp4"
    final_video_path = "final_meme.mp4"

    downloaded_path = None
    download_meta = None

    pinterest_list = load_urls_json(args.pinterest_json, [args.pinterest_url] if args.pinterest_url else [])
    chosen_pinterest = random.choice(pinterest_list) if pinterest_list else args.pinterest_url
    print(f"Scraping Pinterest URL: {chosen_pinterest}")
    downloaded_path = scrape_one_from_pinterest(chosen_pinterest, output_dir=args.pins_dir, num=args.pin_num)
    if not downloaded_path:
        return

    # Download and prepare audio
    audio_clip_path = None
    music_list = load_urls_json(args.music_json, [args.music_playlist_url] if args.music_playlist_url else [])
    chosen_music = random.choice(music_list) if music_list else args.music_playlist_url
    if chosen_music:
        print(f"Downloading random song from playlist: {chosen_music}")
        audio_path = download_random_song_from_playlist(chosen_music, output_dir=args.audio_dir)
        if audio_path:
            print(f"Extracting {args.audio_duration}s random clip from audio")
            audio_clip_path = extract_random_audio_clip(audio_path, clip_duration=args.audio_duration)
            # Clean up original audio file
            if audio_path != audio_clip_path and os.path.exists(audio_path):
                try:
                    os.remove(audio_path)
                    print(f"Cleaned up original audio file: {audio_path}")
                except Exception:
                    pass

    print("Converting to TikTok format...")
    is_youtube = False
    convert_to_tiktok_format(downloaded_path, output_mp4_path, is_youtube=is_youtube, audio_path=audio_clip_path)

    # Generate thumbnail for uploads
    thumbnail_path = "thumbnail.jpg"
    generate_thumbnail(output_mp4_path, thumbnail_path)

    # The final video is the converted one without text
    final_video_path = output_mp4_path

    # Clean up audio clip after conversion
    if audio_clip_path and os.path.exists(audio_clip_path):
        try:
            os.remove(audio_clip_path)
            print(f"Cleaned up audio clip: {audio_clip_path}")
        except Exception:
            pass

    if not args.no_upload and not args.instagram_only:
        if build is None:
            print('YouTube upload dependencies are not installed. Run: pip install google-api-python-client google-auth-oauthlib google-auth-httplib2')
        else:
            try:
                upload_path = output_mp4_path
                if args.force_shorts:
                    try:
                        temp_clip = VideoFileClip(output_mp4_path)
                        if getattr(temp_clip, 'duration', None) and temp_clip.duration > 60:
                            shorts_path = 'final_meme_shorts.mp4'
                            trimmed = temp_clip.with_duration(60)
                            trimmed.write_videofile(shorts_path, fps=24, codec='libx264', audio_codec='aac')
                            upload_path = shorts_path
                            trimmed.close()
                        temp_clip.close()
                    except Exception as e:
                        print(f'Failed to prepare <=60s version: {e}')
                # prepare metadata
                generated = generate_metadata_from_source(chosen_pinterest, download_meta, audio_path)
                upload_title = generated.get('title')
                upload_description = generated.get('description')
                upload_tags = generated.get('tags')
                yt = youtube_authenticate()
                resp = youtube_upload_short(yt, upload_path, upload_title, upload_description, tags=upload_tags, privacyStatus=args.privacy)
                video_id = resp.get('id')
                if video_id:
                    print(f'Uploaded to YouTube: https://youtu.be/{video_id}')
            except Exception as e:
                print(f'YouTube upload failed: {e}')

    # Instagram Reels upload
    if not args.no_upload:
        if Client is None:
            print('Instagram upload dependencies are not installed. Run: pip install instagrapi')
        else:
            instagram_username = os.getenv('INSTAGRAM_USERNAME')
            instagram_password = os.getenv('INSTAGRAM_PASSWORD')
            if not instagram_username or not instagram_password:
                print('Instagram credentials not found in environment variables')
            else:
                try:
                    from instagrapi.exceptions import LoginRequired
                    import logging
                    logger = logging.getLogger()

                    cl = Client()
                    # Add delays to mimic human behavior
                    cl.delay_range = [1, 3]

                    # Try to load existing session
                    session_file = "instagram_session.json"
                    session = None
                    if os.path.exists(session_file):
                        try:
                            session = cl.load_settings(session_file)
                        except Exception as e:
                            logger.info("Could not load session file: %s" % e)
                            session = None

                    login_via_session = False
                    login_via_pw = False

                    if session:
                        try:
                            cl.set_settings(session)
                            cl.login(instagram_username, instagram_password)

                            # Check if session is valid
                            try:
                                cl.get_timeline_feed()
                                login_via_session = True
                            except LoginRequired:
                                logger.info("Session is invalid, need to login via username and password")

                                old_session = cl.get_settings()

                                # Use the same device uuids across logins
                                cl.set_settings({})
                                cl.set_uuids(old_session["uuids"])

                                cl.login(instagram_username, instagram_password)
                                login_via_session = True  # Successfully logged in with password after session
                        except Exception as e:
                            logger.info("Couldn't login user using session information: %s" % e)

                    if not login_via_session:
                        try:
                            logger.info("Attempting to login via username and password. username: %s" % instagram_username)
                            if cl.login(instagram_username, instagram_password):
                                login_via_pw = True
                        except Exception as e:
                            logger.info("Couldn't login user using username and password: %s" % e)

                    if not login_via_pw and not login_via_session:
                        raise Exception("Couldn't login user with either password or session")

                    # Save session for future use
                    cl.dump_settings(session_file)

                    # Generate metadata for Instagram
                    generated = generate_metadata_from_source(chosen_pinterest, download_meta, audio_path)
                    caption = generated.get('description', '')
                    
                    # Remove #Shorts from description for Instagram
                    caption = caption.replace('#Shorts', '').strip()
                    
                    # Add song information if available
                    if audio_clip_path or audio_path:
                        song_title = get_song_title(audio_clip_path or audio_path)
                        if song_title and song_title != audio_clip_path and song_title != audio_path:
                            caption = f"♪ {song_title} ♪\n\n{caption}"
                    
                    # Add hashtags (excluding 'shorts')
                    tags = generated.get('tags', [])
                    # Remove 'shorts' tag if present
                    tags = [tag for tag in tags if tag.lower() != 'shorts']
                    hashtags = ' '.join(f'#{tag}' for tag in tags)
                    if hashtags:
                        caption += f'\n\n{hashtags}'
                    # Upload to Reels
                    media = cl.clip_upload(
                        path=final_video_path,
                        caption=caption,
                        thumbnail=thumbnail_path
                    )
                    print(f'Uploaded to Instagram Reels: https://www.instagram.com/reel/{media.code}/')
                except Exception as e:
                    print(f'Instagram upload failed: {e}')

    # Clean up the temporary files (retry once if locked)
    if downloaded_path and os.path.exists(downloaded_path):
        try:
            os.remove(downloaded_path)
            print(f"Cleaned up temporary file: {downloaded_path}")
        except OSError as e:
            import time
            time.sleep(0.5)
            try:
                os.remove(downloaded_path)
                print(f"Cleaned up temporary file after retry: {downloaded_path}")
            except OSError as e2:
                print(f"Could not remove temporary file: {e2}")

    # Clean up thumbnail
    if thumbnail_path and os.path.exists(thumbnail_path):
        try:
            os.remove(thumbnail_path)
            print(f"Cleaned up thumbnail: {thumbnail_path}")
        except Exception:
            pass

    # Clean up working directories and update .gitignore
    for d in [args.pins_dir, args.audio_dir]:
        try:
            if Path(d).exists():
                shutil.rmtree(d, ignore_errors=True)
                print(f"Removed directory: {d}")
        except Exception as e:
            print(f"Failed to remove directory {d}: {e}")
    ensure_gitignore_entries([f"{args.pins_dir}/", f"{args.audio_dir}/"]) 

if __name__ == "__main__":
    main()