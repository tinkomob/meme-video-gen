import os
import random
from pathlib import Path

def download_random_song_from_playlist(playlist_url: str, output_dir: str = 'audio', audio_format: str = 'mp3'):
    print(f"Downloading from playlist: {playlist_url}", flush=True)
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
        print(f"Found {len(entries)} entries in playlist", flush=True)
        ids = []
        for e in entries:
            if not e:
                continue
            vid = e.get('id') or e.get('url')
            if vid and len(vid) >= 6:
                ids.append(vid)
        print(f"Valid video IDs: {len(ids)}", flush=True)
        if not ids:
            return None
        video_id = random.choice(ids)
        video_url = video_id if video_id.startswith('http') else f'https://www.youtube.com/watch?v={video_id}'
        print(f"Selected video: {video_url}", flush=True)
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
        print(f"Expected audio file: {expected_path}", flush=True)
        if os.path.exists(expected_path):
            print(f"Audio file created successfully: {os.path.getsize(expected_path)} bytes", flush=True)
            return expected_path
        for root, _, files in os.walk(output_dir):
            for f in files:
                if f.startswith(video_id) and f.lower().endswith(f'.{audio_format}'):
                    found_path = os.path.join(root, f)
                    print(f"Found audio file: {found_path}", flush=True)
                    return found_path
        print("No audio file found", flush=True)
        return None
    except Exception as e:
        print(f"Error downloading audio: {e}", flush=True)
        return None

def extract_random_audio_clip(audio_path: str, clip_duration: int = 10, output_path: str | None = None):
    print(f"Extracting audio clip from: {audio_path}", flush=True)
    if not os.path.exists(audio_path):
        print(f"Audio file does not exist: {audio_path}", flush=True)
        return None
    
    file_size = os.path.getsize(audio_path)
    print(f"Audio file size: {file_size} bytes", flush=True)
    if file_size == 0:
        print("Audio file is empty", flush=True)
        return None
    
    try:
        from moviepy.editor import AudioFileClip
        from moviepy.audio.AudioClip import concatenate_audioclips
        clip = AudioFileClip(audio_path)
        print(f"Original audio duration: {clip.duration} seconds", flush=True)
        total = clip.duration
        
        if total <= 0:
            print("Audio file has zero or negative duration", flush=True)
            clip.close()
            return None
        if total < clip_duration:
            start, end = 0, total
            print(f"Audio duration {total:.1f}s is shorter than requested {clip_duration}s", flush=True)
        else:
            max_start = total - clip_duration
            start = random.uniform(0, max_start)
            end = start + clip_duration
            print(f"Extracting clip from {start:.1f}s to {end:.1f}s", flush=True)
        sub = clip.subclip(start, end)
        if not output_path:
            output_path = audio_path.replace('.mp3', '_clip.mp3').replace('.wav', '_clip.wav')
        sub.write_audiofile(output_path, write_logfile=False, logger=None)
        print(f"Audio clip saved to: {output_path} ({sub.duration:.1f}s)", flush=True)
        clip.close()
        sub.close()
        return output_path
    except Exception as e:
        print(f"Error extracting audio clip: {e}", flush=True)
        return None

def get_song_title(audio_path: str):
    try:
        import yt_dlp
        filename = os.path.basename(audio_path)
        video_id = filename.split('.')[0]
        with yt_dlp.YoutubeDL({'quiet': True, 'no_warnings': True}) as ydl:
            info = ydl.extract_info(f'https://www.youtube.com/watch?v={video_id}', download=False)
        author = info.get('uploader') or info.get('channel') or info.get('artist') or 'Unknown Artist'
        title = info.get('title') or video_id
        for token in ['[Official Video]', '[Official Music Video]', '(Official Video)', '(Official Music Video)', '[Audio]', '(Audio)', '[Official Audio]', '(Official Audio)', '[Lyrics]', '(Lyrics)']:
            title = title.replace(token, '')
        title = title.strip()
        if ' - ' in title:
            parts = title.split(' - ', 1)
            author = parts[0].strip()
            song = parts[1].strip()
        elif ' by ' in title.lower():
            idx = title.lower().find(' by ')
            song = title[:idx].strip()
            author = title[idx + 4:].strip()
        elif ' | ' in title:
            parts = title.split(' | ', 1)
            author = parts[0].strip()
            song = parts[1].strip()
        else:
            song = title
        formatted = f"{author} - {song}"
        if len(formatted) > 95:
            max_song = 95 - len(author) - 3
            if max_song > 10:
                song = song[:max_song].strip()
                formatted = f"{author} - {song}"
            else:
                formatted = formatted[:95]
        return formatted
    except Exception:
        name = os.path.splitext(os.path.basename(audio_path))[0]
        return f"Song: {name}"