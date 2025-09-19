import requests
import os
import random
import warnings
import argparse
from dotenv import load_dotenv
from moviepy import VideoFileClip, ImageClip, ColorClip, CompositeVideoClip, TextClip, vfx, concatenate_videoclips

# Load environment variables from .env file
load_dotenv()

# Suppress MoviePy warnings about missing frames
warnings.filterwarnings("ignore", message=".*bytes wanted but 0 bytes read.*")

# API Keys from environment variables
YOUTUBE_API_KEY = os.getenv('YOUTUBE_API_KEY')
INSTAGRAM_ACCESS_TOKEN = os.getenv('INSTAGRAM_ACCESS_TOKEN')

def get_from_meme_api():
    """Get meme from meme-api.com"""
    url = "https://meme-api.com/gimme"
    try:
        response = requests.get(url)
        response.raise_for_status()
        data = response.json()
        if not data.get('nsfw', True):
            return data['url']
    except:
        pass
    return None

def get_from_reddit():
    """Get meme from Reddit r/memes"""
    url = "https://www.reddit.com/r/memes/random.json"
    try:
        response = requests.get(url, headers={'User-Agent': 'meme-generator/1.0'})
        response.raise_for_status()
        data = response.json()
        if isinstance(data, list) and data:
            post = data[0]['data']['children'][0]['data']
            if not post.get('nsfw', True) and post.get('url') and not post['url'].endswith('.gifv'):
                return post['url']
    except:
        pass
    return None

def get_from_instagram():
    """Get meme from Instagram (requires access token)"""
    if not INSTAGRAM_ACCESS_TOKEN:
        return None
    url = f"https://graph.instagram.com/me/media?fields=id,media_type,media_url&access_token={INSTAGRAM_ACCESS_TOKEN}"
    try:
        response = requests.get(url)
        response.raise_for_status()
        data = response.json()
        if 'data' in data and data['data']:
            for item in data['data']:
                if item.get('media_type') == 'IMAGE' and item.get('media_url'):
                    return item['media_url']
    except:
        pass
    return None

def get_from_youtube():
    """Get a random YouTube Short (requires API key)"""
    if not YOUTUBE_API_KEY:
        return None
    url = (
        "https://www.googleapis.com/youtube/v3/search?"
        "part=snippet&type=video&videoDuration=short&"
        "q=memes|funny|meme%20shorts&safeSearch=strict&"
        f"key={YOUTUBE_API_KEY}&maxResults=25"
    )
    try:
        response = requests.get(url)
        response.raise_for_status()
        data = response.json()
        items = data.get('items', [])
        if not items:
            return None
        choice = random.choice(items)
        video_id = choice['id']['videoId']
        # Use watch URL; yt-dlp handles it reliably
        return f"https://www.youtube.com/watch?v={video_id}"
    except Exception:
        return None

def get_random_meme_url(source='all'):
    """
    Fetches a random meme from specified source or all sources.
    """
    source_map = {
        'meme-api': [get_from_meme_api],
        'reddit': [get_from_reddit],
        'instagram': [get_from_instagram],
        'youtube': [get_from_youtube],
        'all': [get_from_meme_api, get_from_reddit, get_from_instagram, get_from_youtube]
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
            local_filename_with_extension = downloaded
        else:
            # Regular download for other URLs
            with requests.get(url, stream=True) as r:
                r.raise_for_status()
                with open(local_filename_with_extension, 'wb') as f:
                    for chunk in r.iter_content(chunk_size=8192):
                        f.write(chunk)
        
        return local_filename_with_extension
    except Exception as e:
        print(f"Error downloading file: {e}")
        return None

def convert_to_tiktok_format(input_path, output_path):
    """
    Converts a GIF or image to a TikTok-formatted MP4 video (9:16).
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
            if base_clip.duration < 10:
                n_loops = int(10 / base_clip.duration) + 1
                clips = [base_clip] * n_loops
                concat_clip = concatenate_videoclips(clips)
                clip = concat_clip.with_duration(10)
            else:
                clip = base_clip  # Keep original duration if >= 10 seconds

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


def main():
    """
    Main function to run the process.
    """
    parser = argparse.ArgumentParser(description='Generate meme videos from various sources')
    parser.add_argument('--source', '-s', 
                       choices=['all', 'meme-api', 'reddit', 'instagram', 'youtube'],
                       default='all',
                       help='Source to get memes from (default: all)')
    
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
    downloaded_path = download_file(meme_url, temp_meme_path)

    if not downloaded_path:
        return

    print("Converting to TikTok format...")
    convert_to_tiktok_format(downloaded_path, output_mp4_path)

    # The final video is the converted one without text
    final_video_path = output_mp4_path

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