import requests
import os
import random
import warnings
import argparse
import json
from dotenv import load_dotenv
from moviepy import VideoFileClip, ImageClip, ColorClip, CompositeVideoClip, TextClip, vfx, concatenate_videoclips
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

def convert_to_tiktok_format(input_path, output_path, is_youtube=False):
    """
    Converts a GIF or image to a TikTok-formatted MP4 video (9:16).
    For YouTube videos, do not loop or cutoff; keep original duration.
    For GIFs/images, loop to at least 10 seconds.
    """
    base_clip = None
    clip = None
    concat_clip = None
    background = None
    final_clip = None
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


def generate_metadata_from_source(source_url: str, download_meta: dict | None):
    title = None
    description = ''
    tags = ['meme', 'funny', 'shorts']
    if download_meta:
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

def main():
    """
    Main function to run the process.
    """
    parser = argparse.ArgumentParser(description='Generate meme videos from various sources and upload to YouTube Shorts by default')
    parser.add_argument('--source', '-s', 
                       choices=['all', 'meme-api', 'youtube'],
                       default='all',
                       help='Source to get memes from (default: all)')
    parser.add_argument('--no-upload', action='store_true', help='Skip uploading the generated video to YouTube Shorts')
    parser.add_argument('--force-shorts', action='store_true', help='Ensure uploaded video is <= 60s by trimming if needed')
    parser.add_argument('--title', default='Funny Meme Short', help='Title for YouTube upload')
    parser.add_argument('--description', default='', help='Description for YouTube upload')
    parser.add_argument('--privacy', default='public', choices=['public','unlisted','private'], help='Privacy status for YouTube upload')
    
    args = parser.parse_args()
    
    print(f"Fetching a random meme from {args.source}...")
    meme_url = get_random_meme_url(args.source)

    if not meme_url:
        return

    print(f"Found meme: {meme_url}")
    temp_meme_path = "temp_meme"
    output_mp4_path = "tiktok_video.mp4"
    final_video_path = "final_meme.mp4"

    print("Downloading meme...")
    downloaded_result = download_file(meme_url, temp_meme_path)
    # download_file may return either a path or (path, meta)
    download_meta = None
    if isinstance(downloaded_result, tuple):
        downloaded_path, download_meta = downloaded_result
    else:
        downloaded_path = downloaded_result

    if not downloaded_path:
        return

    print("Converting to TikTok format...")
    is_youtube = 'youtube.com' in meme_url or 'youtu.be' in meme_url
    convert_to_tiktok_format(downloaded_path, output_mp4_path, is_youtube=is_youtube)

    # The final video is the converted one without text
    final_video_path = output_mp4_path

    if not args.no_upload:
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
                generated = generate_metadata_from_source(meme_url, download_meta)
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

if __name__ == "__main__":
    main()