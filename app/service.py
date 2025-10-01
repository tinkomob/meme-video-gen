import os
os.environ['PYTHONDONTWRITEBYTECODE'] = '1'

import random
import datetime
import uuid
import shutil
from pathlib import Path
from typing import Callable, Optional
from .config import DEFAULT_PINS_DIR, DEFAULT_AUDIO_DIR, DEFAULT_OUTPUT_VIDEO, DEFAULT_THUMBNAIL
from .config import CLIENT_SECRETS, TOKEN_PICKLE, TIKTOK_COOKIES_FILE
from .config import YT_COOKIES_FILE, MAX_PARALLEL_GENERATIONS, DUP_REGEN_RETRIES, TEMP_DIR_MAX_AGE_MINUTES
from .utils import ensure_gitignore_entries, load_urls_json
from .sources import scrape_one_from_pinterest
from .audio import download_random_song_from_playlist, extract_random_audio_clip, get_song_title
from .video import convert_to_tiktok_format, generate_thumbnail
from .metadata import generate_metadata_from_source
from .uploaders import youtube_authenticate, youtube_upload_short, instagram_upload
from .uploaders import tiktok_upload, x_upload
from .debug import set_phase

class GenerationResult:
    def __init__(self, video_path: str | None, thumbnail_path: str | None, source_url: str | None, audio_path: str | None, audio_title: str | None = None):
        self.video_path = video_path
        self.thumbnail_path = thumbnail_path
        self.source_url = source_url
        self.audio_path = audio_path
        self.audio_title = audio_title

def cleanup_old_temp_dirs():
    base = Path('.')
    now = datetime.datetime.utcnow()
    max_age = datetime.timedelta(minutes=TEMP_DIR_MAX_AGE_MINUTES)
    patterns = [f"{DEFAULT_PINS_DIR}_", f"{DEFAULT_AUDIO_DIR}_"]
    removed = 0
    for item in base.iterdir():
        try:
            if not item.is_dir():
                continue
            if not any(item.name.startswith(p) for p in patterns):
                continue
            mtime = datetime.datetime.utcfromtimestamp(item.stat().st_mtime)
            if now - mtime > max_age:
                shutil.rmtree(item, ignore_errors=True)
                removed += 1
        except Exception:
            pass
    return removed

def generate_meme_video(
    pinterest_urls: list[str],
    music_playlists: list[str],
    pin_num: int = 10000,
    audio_duration: int = 10,
    progress: Optional[Callable[[str], None]] = None,
    seed: int | None = None,
    variant_group: int | None = None,
    reddit_sources: list[str] | None = None,
):
    notify = (lambda msg: progress(msg) if callable(progress) else None)
    set_phase('init')
    # use unique ephemeral dirs per generation to allow parallel runs
    unique_id = datetime.datetime.now().strftime('%Y%m%d_%H%M%S_') + os.urandom(3).hex()
    pins_dir = f"{DEFAULT_PINS_DIR}_{unique_id}"
    audio_dir = f"{DEFAULT_AUDIO_DIR}_{unique_id}"
    Path(pins_dir).mkdir(parents=True, exist_ok=True)
    Path(audio_dir).mkdir(parents=True, exist_ok=True)
    
    # Унифицированный список источников с случайным порядком
    sources_candidates: list[tuple[str, Callable[[], tuple[str | None, str | None]]]] = []

    def _pinterest_provider():
        if not pinterest_urls:
            return None, None
        chosen = random.choice(pinterest_urls)
        notify("🔍 Ищу контент на Pinterest…")
        print(f"Trying Pinterest URL: {chosen}", flush=True)
        path = scrape_one_from_pinterest(chosen, output_dir=pins_dir, num=pin_num)
        print(f"Pinterest result: {path}", flush=True)
        return path, chosen if path else None

    def _reddit_provider():
        if not reddit_sources:
            return None, None
        notify("� Пробую Reddit…")
        try:
            from .sources import fetch_one_from_reddit
            path = fetch_one_from_reddit(reddit_sources, output_dir=pins_dir)
            if path:
                # Попытка извлечь сабреддит из имени файла
                base = os.path.basename(path)
                parts = base.split('_')
                sr = parts[1] if len(parts) >= 2 else 'reddit'
                notify("🖼️ Получено изображение с Reddit")
                return path, f"reddit:{sr}"
        except Exception as e:
            print(f"Reddit provider error: {e}", flush=True)
        return None, None

    def _meme_api_provider():
        notify("🧠 Пробую публичный meme API…")
        from .sources import get_from_meme_api
        meme_url = get_from_meme_api()
        print(f"Meme API candidate: {meme_url}", flush=True)
        if not meme_url:
            return None, None
        try:
            import requests
            headers = {'User-Agent': 'Mozilla/5.0'}
            r = requests.get(meme_url, headers=headers, timeout=10)
            r.raise_for_status()
            ext = '.jpg'
            ctype = r.headers.get('content-type', '')
            if 'png' in ctype:
                ext = '.png'
            elif 'gif' in ctype:
                ext = '.gif'
            elif 'webp' in ctype:
                ext = '.webp'
            path = os.path.join(pins_dir, f'meme{ext}')
            with open(path, 'wb') as f:
                f.write(r.content)
            from .utils import add_url_to_history
            add_url_to_history(meme_url)
            notify("🖼️ Мем скачан из meme API")
            return path, meme_url
        except Exception as e:
            print(f"Meme API download error: {e}", flush=True)
            return None, None

    if pinterest_urls:
        sources_candidates.append(("pinterest", _pinterest_provider))
    if reddit_sources:
        sources_candidates.append(("reddit", _reddit_provider))
    # meme API всегда как потенциальный источник
    # sources_candidates.append(("meme_api", _meme_api_provider))

    random.shuffle(sources_candidates)
    downloaded_path = None
    chosen_pinterest = None
    tried = []
    for name, provider in sources_candidates:
        print(f"Trying source provider: {name}", flush=True)
        set_phase(f'source:{name}')
        path, src = provider()
        tried.append(name)
        if path:
            downloaded_path = path
            chosen_pinterest = src
            print(f"Source {name} succeeded with file {path}", flush=True)
            break
        else:
            print(f"Source {name} returned no result, continuing", flush=True)
    print(f"Tried sources order: {tried}", flush=True)
    
    print(f"Final downloaded_path: {downloaded_path}", flush=True)
    if downloaded_path:
        print(f"Downloaded file exists: {os.path.exists(downloaded_path)}", flush=True)
        if os.path.exists(downloaded_path):
            print(f"File size: {os.path.getsize(downloaded_path)} bytes", flush=True)
    
    if not downloaded_path:
        notify("❌ Не удалось получить исходный мем")
        return GenerationResult(None, None, chosen_pinterest, None, None)
    
    # Rest of the function remains the same...
    audio_clip_path = None
    original_audio_path = None
    audio_title = None
    chosen_music = random.choice(music_playlists) if music_playlists else None
    print(f"Selected music playlist: {chosen_music}", flush=True)
    if chosen_music:
        set_phase('audio_download')
        notify("🎵 Скачиваю трек из плейлиста…")
        audio_path = download_random_song_from_playlist(chosen_music, output_dir=audio_dir)
        print(f"Downloaded audio path: {audio_path}", flush=True)
        if audio_path:
            original_audio_path = audio_path
            # Get audio title before processing
            audio_title = get_song_title(audio_path)
            notify("✂️ Вырезаю аудио-клип нужной длительности…")
            set_phase('audio_clip')
            audio_clip_path = extract_random_audio_clip(audio_path, clip_duration=audio_duration)
            print(f"Extracted audio clip path: {audio_clip_path}", flush=True)
            if audio_path != audio_clip_path and os.path.exists(audio_path):
                try:
                    os.remove(audio_path)
                except Exception:
                    pass
    # create unique output/thumbnail names to avoid overwriting when generating multiple candidates
    unique_suffix = datetime.datetime.now().strftime('%Y%m%d_%H%M%S_') + os.urandom(3).hex()
    output_path = f"tiktok_video_{unique_suffix}.mp4"
    set_phase('video_convert')
    notify("🎬 Конвертирую видео в формат TikTok…")
    print(f"Starting video conversion with downloaded_path: {downloaded_path}, output_path: {output_path}", flush=True)
    result_path = convert_to_tiktok_format(downloaded_path, output_path, is_youtube=False, audio_path=audio_clip_path, seed=seed, variant_group=variant_group)
    print(f"Video conversion result: {result_path}", flush=True)
    if not result_path or not os.path.exists(result_path):
        print("Video conversion failed", flush=True)
        notify("❌ Ошибка при конвертации видео")
        return GenerationResult(None, None, chosen_pinterest, None, audio_title)
    
    thumbnail_path = f"thumbnail_{unique_suffix}.jpg"
    set_phase('thumbnail')
    notify("🖼️ Генерирую миниатюру…")
    print(f"Generating thumbnail for: {output_path}", flush=True)
    thumb_result = generate_thumbnail(output_path, thumbnail_path)
    print(f"Thumbnail generation result: {thumb_result}", flush=True)
    if not thumb_result or not os.path.exists(thumb_result):
        print("Thumbnail generation failed", flush=True)
        notify("❌ Не удалось создать миниатюру")
        return GenerationResult(None, None, chosen_pinterest, None, audio_title)
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
    ensure_gitignore_entries([
        f"{DEFAULT_PINS_DIR}_*/",
        f"{DEFAULT_AUDIO_DIR}_*/",
        "tiktok_video_*.mp4",
        "thumbnail_*.jpg"
    ]) 
    set_phase('done')
    notify("✅ Готово! Видео и миниатюра созданы")
    return GenerationResult(output_path, thumbnail_path, chosen_pinterest, original_audio_path, audio_title)

def deploy_to_socials(
    video_path: str,
    thumbnail_path: str,
    source_url: str,
    audio_path: str | None,
    privacy: str = 'public',
    socials: list[str] | None = None,
    dry_run: bool = False,
    progress: Optional[Callable[[str], None]] = None,
):
    notify = (lambda msg: progress(msg) if callable(progress) else None)
    generated = generate_metadata_from_source(source_url, None, audio_path)

    def _song_from_title_fallback(val: str | None) -> str | None:
        try:
            if not val:
                return None
            t = val.replace('#Shorts', '').strip()
            if ' - ' in t:
                return t
            return None
        except Exception:
            return None
    
    # Default to all socials if none specified
    if socials is None:
        socials = ['youtube', 'instagram', 'tiktok', 'x']
    
    # Normalize social names to lowercase
    socials = [s.lower() for s in socials]
    
    yt_link = None
    if 'youtube' in socials:
        if dry_run:
            yt_link = f"https://youtu.be/dry-run-{generated['title'].replace(' ', '-').lower()}"
            print(f"DRY RUN: Would upload to YouTube with title: {generated['title']}", flush=True)
        else:
            if not (os.path.exists(CLIENT_SECRETS) and os.path.exists(TOKEN_PICKLE)):
                missing = []
                if not os.path.exists(CLIENT_SECRETS):
                    missing.append("client_secrets.json")
                if not os.path.exists(TOKEN_PICKLE):
                    missing.append("token.pickle")
                notify(
                    "⚠️ YouTube: отсутствуют обязательные файлы: "
                    + ", ".join(missing)
                    + ".\nЗагрузите их командами: /uploadclient (client_secrets.json) и /uploadtoken (token.pickle)."
                )
                notify("⏭️ YouTube — пропущено из‑за отсутствия файлов")
            else:
                yt = youtube_authenticate()
                if yt is not None:
                    try:
                        resp = youtube_upload_short(yt, video_path, generated['title'], generated['description'], tags=generated['tags'], privacyStatus=privacy)
                    except Exception as e:
                        resp = None
                        notify(f"❌ YouTube: ошибка загрузки — {e}")
                    if resp and resp.get('id'):
                        yt_link = f"https://youtu.be/{resp.get('id')}"
                    else:
                        if yt_link is None:
                            notify("❌ YouTube: загрузка не удалась")
                else:
                    notify("❌ YouTube: аутентификация не удалась")
    
    insta_link = None
    if 'instagram' in socials:
        if dry_run:
            insta_link = f"https://www.instagram.com/reel/dry-run-{generated['title'].replace(' ', '-').lower()}/"
            print(f"DRY RUN: Would upload to Instagram with caption: {generated.get('description', '')[:50]}...", flush=True)
        else:
            caption = generated.get('description', '').replace('#Shorts', '').strip()
            song_title = None
            if audio_path:
                song_title = get_song_title(audio_path)
            if not song_title:
                song_title = _song_from_title_fallback(generated.get('title'))
            if song_title:
                caption = f"♪ {song_title} ♪\n\n{caption}"
            tags = [t for t in generated.get('tags', []) if t.lower() != 'shorts']
            if tags:
                caption += ('\n\n' + ' '.join(f'#{t}' for t in tags))
            insta = instagram_upload(video_path, caption, thumbnail=thumbnail_path)
            try:
                if insta:
                    code = None
                    if hasattr(insta, 'code'):
                        code = getattr(insta, 'code')
                    elif isinstance(insta, dict):
                        if 'error' in insta:
                            err = insta.get('error') or 'Ошибка'
                            det = insta.get('details') or ''
                            notify(f"❌ Instagram: {err}{(': ' + det) if det else ''}")
                        code = insta.get('code')
                        if not code and 'url' in insta and isinstance(insta['url'], str):
                            insta_link = insta['url']
                    if code and not insta_link:
                        insta_link = f"https://www.instagram.com/reel/{code}/"
                else:
                    notify("❌ Instagram: загрузка не удалась")
            except Exception:
                notify("❌ Instagram: непредвиденная ошибка при обработке ответа")

    tiktok_link = None
    if 'tiktok' in socials:
        if dry_run:
            tiktok_link = f"https://www.tiktok.com/dry-run/{generated['title'].replace(' ', '-').lower()}"
            print(f"DRY RUN: Would upload to TikTok with description: {generated.get('description', '')[:50]}...", flush=True)
        else:
            if not os.path.exists(TIKTOK_COOKIES_FILE):
                notify(
                    "⚠️ TikTok: не найден cookies.txt. Загрузите cookies.txt как документ командой /uploadcookies"
                )
                notify("⏭️ TikTok — пропущено из‑за отсутствия cookies.txt")
            else:
                desc = generated.get('description', '')
                song_title = None
                if audio_path:
                    song_title = get_song_title(audio_path)
                if not song_title:
                    song_title = _song_from_title_fallback(generated.get('title'))
                if song_title:
                    desc = f"♪ {song_title} ♪\n\n{desc}"
                try:
                    resp = tiktok_upload(video_path, description=desc, cover=thumbnail_path)
                    if resp:
                        if isinstance(resp, dict):
                            if 'url' in resp:
                                tiktok_link = resp['url']
                                print(f"TikTok upload successful: {tiktok_link}")
                            elif resp.get('success'):
                                tiktok_link = resp.get('url', f'https://www.tiktok.com/@user/video/{generated["title"].replace(" ", "-")[:20]}')
                                print(f"TikTok upload likely successful (WebDriver error handled): {tiktok_link}")
                            else:
                                print(f"TikTok upload response: {resp}")
                                err = resp.get('error') if isinstance(resp, dict) else None
                                det = resp.get('details') if isinstance(resp, dict) else None
                                if err or det:
                                    notify(f"❌ TikTok: {err or 'ошибка'}{(': ' + det) if det else ''}")
                                else:
                                    notify("❌ TikTok: загрузка не удалась")
                        else:
                            print(f"TikTok upload returned non-dict response: {type(resp)}")
                            notify("❌ TikTok: неожиданный ответ от сервиса")
                    else:
                        print("TikTok upload returned None")
                        notify("❌ TikTok: загрузка не удалась")
                except Exception as e:
                    print(f"TikTok upload exception: {e}", flush=True)
                    notify(f"❌ TikTok: исключение при загрузке — {e}")

    x_link = None
    if 'x' in socials or 'twitter' in socials:
        if dry_run:
            x_link = f"https://x.com/user/status/dry-run-{generated['title'].replace(' ', '-').lower()}"
            print(f"DRY RUN: Would upload to X with text: {generated.get('description', '')[:50]}...", flush=True)
        else:
            text = generated.get('description', '')
            song_title = None
            if audio_path:
                song_title = get_song_title(audio_path)
            if not song_title:
                song_title = _song_from_title_fallback(generated.get('title'))
            if song_title:
                song_prefix = f"♪ {song_title} ♪\n\n"
                if len(song_prefix + text) <= 280:
                    text = song_prefix + text
                else:
                    if len(song_prefix) <= 280:
                        text = song_prefix
            try:
                resp = x_upload(video_path, text)
                tweet_id = None
                if resp:
                    data_obj = getattr(resp, 'data', None)
                    if isinstance(data_obj, dict) and 'id' in data_obj:
                        tweet_id = data_obj['id']
                    elif isinstance(resp, dict):
                        data_dict = resp.get('data')
                        if isinstance(data_dict, dict) and 'id' in data_dict:
                            tweet_id = data_dict['id']
                if tweet_id:
                    x_link = f"https://x.com/user/status/{tweet_id}"
                    print(f"X upload successful: {x_link}")
                else:
                    print("X upload returned None or missing id")
                    notify("❌ X: загрузка не удалась")
            except Exception as e:
                print(f"X upload exception: {e}", flush=True)
                notify(f"❌ X: исключение при загрузке — {e}")

    return {'youtube': yt_link, 'instagram': insta_link, 'tiktok': tiktok_link, 'x': x_link}

def replace_audio_in_video(
    video_path: str,
    music_playlists: list[str],
    audio_duration: int = 12,
    progress: Optional[Callable[[str], None]] = None,
):
    notify = (lambda msg: progress(msg) if callable(progress) else None)
    
    if not os.path.exists(video_path):
        notify("❌ Видео файл не найден")
        return None
    
    if not music_playlists:
        notify("❌ Список плейлистов пуст")
        return None
    
    unique_id = datetime.datetime.now().strftime('%Y%m%d_%H%M%S_') + os.urandom(3).hex()
    audio_dir = f"{DEFAULT_AUDIO_DIR}_{unique_id}"
    Path(audio_dir).mkdir(parents=True, exist_ok=True)
    
    try:
        chosen_music = random.choice(music_playlists)
        notify("🎵 Скачиваю новый трек из плейлиста…")
        set_phase('audio_download')
        
        audio_path = download_random_song_from_playlist(chosen_music, output_dir=audio_dir)
        if not audio_path:
            notify("❌ Не удалось скачать аудио")
            return None
        
        notify("✂️ Вырезаю аудио-клип нужной длительности…")
        set_phase('audio_clip')
        audio_clip_path = extract_random_audio_clip(audio_path, clip_duration=audio_duration)
        
        if not audio_clip_path:
            notify("❌ Не удалось вырезать аудио-клип")
            return None
        
        # Создаем новое видео с замененным аудио
        unique_suffix = datetime.datetime.now().strftime('%Y%m%d_%H%M%S_') + os.urandom(3).hex()
        new_video_path = f"tiktok_video_{unique_suffix}.mp4"
        
        notify("🎬 Заменяю аудио в видео…")
        set_phase('audio_replace')
        
        try:
            from moviepy.editor import VideoFileClip, AudioFileClip
            
            video_clip = VideoFileClip(video_path)
            new_audio = AudioFileClip(audio_clip_path)
            
            # Обрезаем аудио по длительности видео
            video_duration = video_clip.duration
            if new_audio.duration > video_duration:
                new_audio = new_audio.subclip(0, video_duration)
            
            # Заменяем аудио
            final_video = video_clip.set_audio(new_audio)
            final_video.write_videofile(new_video_path, verbose=False, logger=None)
            
            # Освобождаем ресурсы
            video_clip.close()
            new_audio.close() 
            final_video.close()
            
        except Exception as e:
            notify(f"❌ Ошибка при замене аудио: {e}")
            return None
        
        # Получаем название трека
        audio_title = get_song_title(audio_path)
        
        # Создаем новую миниатюру
        thumbnail_path = f"thumbnail_{unique_suffix}.jpg"
        notify("🖼️ Генерирую новую миниатюру…")
        set_phase('thumbnail')
        thumb_result = generate_thumbnail(new_video_path, thumbnail_path)
        
        if not thumb_result or not os.path.exists(thumb_result):
            notify("❌ Не удалось создать миниатюру")
            return None
        
        # Очистка временных файлов
        if audio_clip_path and os.path.exists(audio_clip_path):
            try:
                os.remove(audio_clip_path)
            except Exception:
                pass
        if audio_path and os.path.exists(audio_path):
            try:
                os.remove(audio_path)
            except Exception:
                pass
        
        # Удаляем временную директорию
        try:
            if Path(audio_dir).exists():
                shutil.rmtree(audio_dir, ignore_errors=True)
        except Exception:
            pass
        
        set_phase('done')
        notify("✅ Аудио успешно заменено!")
        
        return GenerationResult(new_video_path, thumbnail_path, None, None, audio_title)
        
    except Exception as e:
        notify(f"❌ Общая ошибка при замене аудио: {e}")
        return None
    finally:
        # Очистка в случае ошибки
        try:
            if Path(audio_dir).exists():
                shutil.rmtree(audio_dir, ignore_errors=True)
        except Exception:
            pass
