import os
import random
from pathlib import Path
from typing import Any

def _build_ytdlp_opts(base_opts: dict[str, Any] | None = None) -> dict[str, Any]:
    opts: dict[str, Any] = {
        'quiet': True,
        'no_warnings': True,
        'retries': 3,
        'fragment_retries': 3,
        'extractor_retries': 2,
        'retry_sleep': '3,6,10',
    }
    if base_opts:
        opts.update(base_opts)
    cookies_file = os.getenv('YT_COOKIES_FILE') or os.getenv('YTDLP_COOKIES_FILE')
    if not cookies_file and os.path.isfile('youtube_cookies.txt'):
        cookies_file = 'youtube_cookies.txt'
    if cookies_file:
        if os.path.isfile(cookies_file):
            file_size = os.path.getsize(cookies_file)
            print(f"Using YouTube cookies file: {cookies_file} ({file_size} bytes)", flush=True)
            opts['cookiefile'] = cookies_file
        elif os.path.exists(cookies_file) and not os.path.isfile(cookies_file):
            print(f"youtube cookies path exists but is not a file: {cookies_file}", flush=True)
        else:
            print(f"YouTube cookies file not found: {cookies_file}", flush=True)
    else:
        print("No YouTube cookies file configured, trying browser extraction", flush=True)
        browser = os.getenv('YT_COOKIES_FROM_BROWSER') or os.getenv('YTDLP_COOKIES_FROM_BROWSER')
        profile = os.getenv('YT_COOKIES_PROFILE') or os.getenv('YTDLP_COOKIES_PROFILE')
        if browser:
            try:
                if profile:
                    opts['cookiesfrombrowser'] = (browser, None, profile, None)
                else:
                    opts['cookiesfrombrowser'] = (browser,)
            except Exception:
                pass
    ua = os.getenv('YT_USER_AGENT')
    if ua:
        opts['user_agent'] = ua
    else:
        opts['user_agent'] = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
    opts['http_headers'] = {
        'User-Agent': opts['user_agent'],
        'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8',
        'Accept-Language': 'en-US,en;q=0.9,ru;q=0.8',
        'Sec-Fetch-Mode': 'navigate',
        'Sec-Fetch-Dest': 'document',
        'Upgrade-Insecure-Requests': '1',
        'Referer': 'https://www.youtube.com/',
    }
    
    opts['extractor_args'] = {
        'youtube': {
            'player_client': ['android', 'web'],
            'player_skip': ['webpage', 'configs'],
            'player_params': ['cbr=Chrome&cbrver=120.0.0.0'],
            'po_token': ['1'],
        }
    }
    
    impersonate = os.getenv('YT_IMPERSONATE')
    if impersonate and False:  # Disabled by default due to curl-cffi issues on Windows
        # Impersonate support is available but disabled to avoid platform-specific issues
        try:
            from yt_dlp.networking.impersonate import ImpersonateTarget
            target = ImpersonateTarget.from_str(alias)
            try:
                from yt_dlp.networking._curlcffi import CurlCFFIRH
                supported = list(getattr(CurlCFFIRH, 'supported_targets', ()) or [])
            except ImportError:
                supported = []
            if supported:
                def pick(t):
                    for s in supported:
                        if t in s:
                            return s
                    return None
                resolved = pick(target)
                if not resolved and target.client:
                    for s in supported:
                        if (s.client or '').lower() == target.client.lower():
                            resolved = s
                            break
                if resolved:
                    target = resolved
                    opts['impersonate'] = target
        except Exception as e:
            print(f"Note: Impersonate mode unavailable: {e}", flush=True)
    return opts

def download_random_song_from_playlist_fallback(playlist_url: str, output_dir: str = 'audio', audio_format: str = 'mp3'):
    """Fallback download function without impersonate mode"""
    print(f"Fallback: Downloading from playlist without impersonate: {playlist_url}", flush=True)
    import yt_dlp
    try:
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        # Build opts without impersonate
        list_opts = {
            'quiet': True,
            'no_warnings': True,
            'retries': 5,
            'fragment_retries': 5,
            'extractor_retries': 3,
            'retry_sleep': '3,6,10',
            'skip_download': True,
            'extract_flat': 'in_playlist',
            'ignoreerrors': True,
            'user_agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36',
        }
        cookies_file = os.getenv('YT_COOKIES_FILE') or os.getenv('YTDLP_COOKIES_FILE')
        if not cookies_file and os.path.isfile('youtube_cookies.txt'):
            cookies_file = 'youtube_cookies.txt'
        if cookies_file and os.path.isfile(cookies_file):
            list_opts['cookiefile'] = cookies_file
            print(f"Using cookies file: {cookies_file}", flush=True)
        
        with yt_dlp.YoutubeDL(list_opts) as ydl:  # type: ignore[arg-type]
            info = ydl.extract_info(playlist_url, download=False)
        entries = (info or {}).get('entries') or []
        print(f"Fallback: Found {len(entries)} entries", flush=True)
        ids = []
        for e in entries:
            if not e:
                continue
            vid = e.get('id') or e.get('url')
            if vid and len(vid) >= 6:
                ids.append(vid)
        if not ids:
            raise ValueError("No video IDs found in playlist (fallback)")
        video_id = random.choice(ids)
        video_url = video_id if video_id.startswith('http') else f'https://www.youtube.com/watch?v={video_id}'
        print(f"Fallback: Selected video: {video_url}", flush=True)
        
        ydl_opts = {
            'quiet': True,
            'no_warnings': True,
            'retries': 5,
            'fragment_retries': 5,
            'extractor_retries': 3,
            'retry_sleep': '3,6,10',
            'format': 'bestaudio/best',
            'outtmpl': os.path.join(output_dir, '%(id)s.%(ext)s'),
            'postprocessors': [{
                'key': 'FFmpegExtractAudio',
                'preferredcodec': audio_format,
                'preferredquality': '192',
            }],
            'user_agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36',
        }
        if cookies_file and os.path.isfile(cookies_file):
            ydl_opts['cookiefile'] = cookies_file
        
        with yt_dlp.YoutubeDL(ydl_opts) as ydl:  # type: ignore[arg-type]
            result = ydl.download([video_url])
            if result != 0:
                raise RuntimeError(f"yt-dlp returned error code: {result}")
        
        expected_path = os.path.join(output_dir, f"{video_id}.{audio_format}")
        if os.path.exists(expected_path):
            file_size = os.path.getsize(expected_path)
            if file_size > 0:
                print(f"Fallback: Audio file created: {file_size} bytes", flush=True)
                return expected_path
        
        # Search for file
        for root, _, files in os.walk(output_dir):
            for f in files:
                if f.startswith(video_id) and f.lower().endswith(f'.{audio_format}'):
                    found_path = os.path.join(root, f)
                    if os.path.getsize(found_path) > 0:
                        print(f"Fallback: Found audio file: {found_path}", flush=True)
                        return found_path
        raise FileNotFoundError("Audio file not created after download (fallback)")
    except Exception as e:
        print(f"Fallback failed: {e}", flush=True)
        raise

def download_random_song_from_playlist(playlist_url: str, output_dir: str = 'audio', audio_format: str = 'mp3'):
    print(f"Downloading from playlist: {playlist_url}", flush=True)
    import yt_dlp
    try:
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        list_opts = _build_ytdlp_opts({
            'skip_download': True,
            'extract_flat': 'in_playlist',
            'ignoreerrors': True,
        })
        with yt_dlp.YoutubeDL(list_opts) as ydl:  # type: ignore[arg-type]
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
            error_msg = "Не найдены видео в плейлисте"
            print(error_msg, flush=True)
            raise ValueError(error_msg)
        video_id = random.choice(ids)
        video_url = video_id if video_id.startswith('http') else f'https://www.youtube.com/watch?v={video_id}'
        print(f"Selected video: {video_url}", flush=True)
        ydl_opts = _build_ytdlp_opts({
            'format': 'bestaudio/best',
            'outtmpl': os.path.join(output_dir, '%(id)s.%(ext)s'),
            'postprocessors': [{
                'key': 'FFmpegExtractAudio',
                'preferredcodec': audio_format,
                'preferredquality': '192',
            }],
        })
        with yt_dlp.YoutubeDL(ydl_opts) as ydl:  # type: ignore[arg-type]
            result = ydl.download([video_url])
            if result != 0:
                error_msg = f"yt-dlp вернул код ошибки: {result}"
                print(error_msg, flush=True)
                raise RuntimeError(error_msg)
        expected_path = os.path.join(output_dir, f"{video_id}.{audio_format}")
        print(f"Expected audio file: {expected_path}", flush=True)
        if os.path.exists(expected_path):
            file_size = os.path.getsize(expected_path)
            if file_size == 0:
                error_msg = "Загружен пустой аудио-файл"
                print(error_msg, flush=True)
                raise ValueError(error_msg)
            print(f"Audio file created successfully: {file_size} bytes", flush=True)
            return expected_path
        for root, _, files in os.walk(output_dir):
            for f in files:
                if f.startswith(video_id) and f.lower().endswith(f'.{audio_format}'):
                    found_path = os.path.join(root, f)
                    file_size = os.path.getsize(found_path)
                    if file_size == 0:
                        error_msg = "Найден пустой аудио-файл"
                        print(error_msg, flush=True)
                        raise ValueError(error_msg)
                    print(f"Found audio file: {found_path} ({file_size} bytes)", flush=True)
                    return found_path
        error_msg = "Аудио-файл не был создан после загрузки"
        print(error_msg, flush=True)
        raise FileNotFoundError(error_msg)
    except Exception as e:
        error_detail = str(e)
        # Handle impersonate-specific errors
        if 'Impersonate target' in error_detail and 'not available' in error_detail:
            error_msg = "Ошибка режима impersonate. Попытка скачивания без impersonate..."
            print(error_msg, flush=True)
            # Retry without impersonate
            try:
                return download_random_song_from_playlist_fallback(playlist_url, output_dir, audio_format)
            except Exception as retry_error:
                raise RuntimeError(f"Ошибка при загрузке аудио (даже с fallback): {retry_error}") from retry_error
        elif 'HTTP Error 403' in error_detail:
            error_msg = "Ошибка доступа к YouTube (403). Возможно, нужны cookies или YouTube заблокировал запрос."
        elif 'Video unavailable' in error_detail or 'Private video' in error_detail:
            error_msg = "Видео недоступно или приватное"
        elif 'Sign in to confirm' in error_detail:
            error_msg = "YouTube требует авторизацию. Необходимы cookies (youtube_cookies.txt)"
        elif 'No video formats' in error_detail:
            error_msg = "Не найдены доступные форматы аудио"
        else:
            error_msg = f"Ошибка при загрузке аудио: {error_detail}"
        print(error_msg, flush=True)
        raise RuntimeError(error_msg) from e

def extract_random_audio_clip(audio_path: str, clip_duration: int = 10, output_path: str | None = None):
    print(f"Extracting audio clip from: {audio_path}", flush=True)
    if not os.path.exists(audio_path):
        error_msg = f"Аудио-файл не существует: {audio_path}"
        print(error_msg, flush=True)
        raise FileNotFoundError(error_msg)
    
    file_size = os.path.getsize(audio_path)
    print(f"Audio file size: {file_size} bytes", flush=True)
    if file_size == 0:
        error_msg = "Аудио-файл пустой (0 байт)"
        print(error_msg, flush=True)
        raise ValueError(error_msg)
    
    try:
        from moviepy.editor import AudioFileClip
        from moviepy.audio.AudioClip import concatenate_audioclips
        clip = AudioFileClip(audio_path)
        print(f"Original audio duration: {clip.duration} seconds", flush=True)
        total = clip.duration
        
        if total <= 0:
            error_msg = "Аудио-файл имеет нулевую или отрицательную длительность"
            print(error_msg, flush=True)
            clip.close()
            raise ValueError(error_msg)
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
        
        if not os.path.exists(output_path):
            error_msg = "Не удалось создать аудио-клип"
            print(error_msg, flush=True)
            raise RuntimeError(error_msg)
        
        clip_size = os.path.getsize(output_path)
        if clip_size == 0:
            error_msg = "Созданный аудио-клип пустой"
            print(error_msg, flush=True)
            raise ValueError(error_msg)
        
        print(f"Audio clip saved to: {output_path} ({sub.duration:.1f}s, {clip_size} bytes)", flush=True)
        clip.close()
        sub.close()
        return output_path
    except Exception as e:
        error_msg = f"Ошибка при вырезании аудио-клипа: {str(e)}"
        print(error_msg, flush=True)
        raise RuntimeError(error_msg) from e

def get_song_title(audio_path: str):
    try:
        import yt_dlp
        filename = os.path.basename(audio_path)
        video_id = filename.split('.')[0]
        with yt_dlp.YoutubeDL(_build_ytdlp_opts()) as ydl:  # type: ignore[arg-type]
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
        if author.endswith(' - Topic'):
            author = author[:-8].strip()
        if song.startswith('Topic - '):
            song = song[8:].strip()
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