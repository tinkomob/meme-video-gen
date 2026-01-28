import os
import random
import hashlib
import json
from pathlib import Path
from typing import Any

# File for tracking audio files SHA256 hashes to prevent duplicates
AUDIO_HASH_INDEX = "audio_hash_index.json"

def _load_audio_hash_index() -> dict[str, str]:
    """Load the audio hash index from disk"""
    if not os.path.exists(AUDIO_HASH_INDEX):
        return {}
    try:
        with open(AUDIO_HASH_INDEX, 'r', encoding='utf-8') as f:
            return json.load(f)
    except Exception:
        return {}

def _save_audio_hash_index(index: dict[str, str]) -> None:
    """Save the audio hash index to disk"""
    try:
        with open(AUDIO_HASH_INDEX, 'w', encoding='utf-8') as f:
            json.dump(index, f, ensure_ascii=False, indent=2)
    except Exception as e:
        print(f"Warning: Failed to save audio hash index: {e}", flush=True)

def _calculate_file_hash(file_path: str) -> str:
    """Calculate SHA256 hash of a file"""
    sha256_hash = hashlib.sha256()
    try:
        with open(file_path, 'rb') as f:
            for byte_block in iter(lambda: f.read(4096), b""):
                sha256_hash.update(byte_block)
        return sha256_hash.hexdigest()
    except Exception as e:
        print(f"Warning: Failed to calculate hash for {file_path}: {e}", flush=True)
        return ""

def _check_audio_duplicate(file_path: str) -> bool:
    """Check if audio file with same hash already exists in index"""
    if not os.path.exists(file_path):
        return False
    
    file_hash = _calculate_file_hash(file_path)
    if not file_hash:
        return False
    
    index = _load_audio_hash_index()
    return file_hash in index.values()

def _register_audio_file(file_path: str, identifier: str = "") -> bool:
    """Register audio file in hash index to prevent future duplicates"""
    if not os.path.exists(file_path):
        return False
    
    file_hash = _calculate_file_hash(file_path)
    if not file_hash:
        return False
    
    index = _load_audio_hash_index()
    # Use file path or identifier as key
    key = identifier or os.path.abspath(file_path)
    
    if file_hash not in index.values():
        index[key] = file_hash
        _save_audio_hash_index(index)
        return True
    return False

def _build_ytdlp_opts(base_opts: dict[str, Any] | None = None) -> dict[str, Any]:
    opts: dict[str, Any] = {
        'quiet': True,
        'no_warnings': True,
        'retries': 5,
        'fragment_retries': 5,
        'extractor_retries': 3,
        'retry_sleep': '3,6,10',
        'socket_timeout': 60,
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
        'Connection': 'keep-alive',
    }
    
    # Network optimization
    opts['socket_timeout'] = 60
    opts['connection_timeout'] = 60
    opts['source_address'] = '0.0.0.0'  # Bind to all available interfaces
    opts['verbose'] = False
    
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
            'socket_timeout': 60,
            'connection_timeout': 60,
            'format': 'bestaudio[ext=m4a]/bestaudio[ext=webm]/bestaudio/best',
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
            'format': 'bestaudio[ext=m4a]/bestaudio[ext=webm]/bestaudio/best',
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
        elif 'Read timed out' in error_detail or 'Connection' in error_detail:
            error_msg = f"Сетевая ошибка при загрузке: {error_detail[:100]}. Попробуйте позже."
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

def search_tracks_in_playlists(music_playlists: list[str], search_query: str, max_results: int = 10):
    """
    Поиск треков в плейлистах YouTube по запросу
    Возвращает список кортежей (video_id, title, author)
    """
    print(f"Searching for '{search_query}' in {len(music_playlists)} playlists", flush=True)
    import yt_dlp
    
    results = []
    search_lower = search_query.lower()
    
    for playlist_url in music_playlists:
        try:
            list_opts = _build_ytdlp_opts({
                'skip_download': True,
                'extract_flat': 'in_playlist',
                'ignoreerrors': True,
            })
            
            with yt_dlp.YoutubeDL(list_opts) as ydl:  # type: ignore[arg-type]
                info = ydl.extract_info(playlist_url, download=False)
            
            entries = (info or {}).get('entries') or []
            
            for e in entries:
                if not e:
                    continue
                
                video_id = e.get('id') or e.get('url')
                title = e.get('title', '')
                uploader = e.get('uploader', '') or e.get('channel', '')
                
                if not video_id or not title:
                    continue
                
                # Проверяем совпадение с запросом
                if search_lower in title.lower() or (uploader and search_lower in uploader.lower()):
                    results.append((video_id, title, uploader))
                    
                    if len(results) >= max_results:
                        return results
        
        except Exception as e:
            print(f"Error searching in playlist {playlist_url}: {e}", flush=True)
            continue
    
    return results

def download_specific_track(video_id: str, output_dir: str = 'audio', audio_format: str = 'mp3'):
    """
    Скачивает конкретный трек по video_id
    Возвращает путь к скачанному аудио файлу
    """
    print(f"Downloading specific track: {video_id}", flush=True)
    import yt_dlp
    
    try:
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        
        video_url = video_id if video_id.startswith('http') else f'https://www.youtube.com/watch?v={video_id}'
        
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
                raise RuntimeError(f"yt-dlp вернул код ошибки: {result}")
        
        # Извлекаем чистый video_id
        clean_id = video_id.split('?')[0].split('/')[-1] if 'youtube.com' in video_id or 'youtu.be' in video_id else video_id
        expected_path = os.path.join(output_dir, f"{clean_id}.{audio_format}")
        
        if os.path.exists(expected_path):
            file_size = os.path.getsize(expected_path)
            if file_size > 0:
                print(f"Track downloaded successfully: {file_size} bytes", flush=True)
                return expected_path
        
        # Поиск файла
        for root, _, files in os.walk(output_dir):
            for f in files:
                if clean_id in f and f.lower().endswith(f'.{audio_format}'):
                    found_path = os.path.join(root, f)
                    if os.path.getsize(found_path) > 0:
                        print(f"Found track file: {found_path}", flush=True)
                        return found_path
        
        raise FileNotFoundError("Аудио-файл не был создан после загрузки")
        
    except Exception as e:
        error_msg = f"Ошибка при загрузке трека: {str(e)}"
        print(error_msg, flush=True)
        raise RuntimeError(error_msg) from e

def extract_audio_from_file(input_path: str, output_dir: str = 'audio', audio_format: str = 'mp3'):
    """
    Извлекает аудио из видео или конвертирует аудио файл в нужный формат
    Возвращает путь к извлеченному аудио файлу
    """
    print(f"Extracting audio from: {input_path}", flush=True)
    
    if not os.path.exists(input_path):
        raise FileNotFoundError(f"Файл не найден: {input_path}")
    
    Path(output_dir).mkdir(parents=True, exist_ok=True)
    
    import uuid
    unique_id = str(uuid.uuid4())[:8]
    output_path = os.path.join(output_dir, f"uploaded_audio_{unique_id}.{audio_format}")
    
    try:
        from moviepy.editor import VideoFileClip, AudioFileClip
        
        # Пытаемся открыть как видео
        try:
            video_clip = VideoFileClip(input_path)
            if video_clip.audio is None:
                video_clip.close()
                raise ValueError("Видео не содержит аудио дорожку")
            
            video_clip.audio.write_audiofile(output_path, write_logfile=False, logger=None)
            video_clip.close()
            
        except Exception:
            # Если не получилось как видео, пробуем как аудио
            audio_clip = AudioFileClip(input_path)
            audio_clip.write_audiofile(output_path, write_logfile=False, logger=None)
            audio_clip.close()
        
        if not os.path.exists(output_path) or os.path.getsize(output_path) == 0:
            raise RuntimeError("Не удалось извлечь аудио")
        
        print(f"Audio extracted to: {output_path}", flush=True)
        return output_path
        
    except Exception as e:
        error_msg = f"Ошибка при извлечении аудио: {str(e)}"
        print(error_msg, flush=True)
        raise RuntimeError(error_msg) from e