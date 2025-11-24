import os
os.environ['PYTHONDONTWRITEBYTECODE'] = '1'

import random
import logging
import datetime
import uuid
import shutil
from pathlib import Path
from typing import Callable, Optional
from .config import DEFAULT_PINS_DIR, DEFAULT_AUDIO_DIR, DEFAULT_OUTPUT_VIDEO, DEFAULT_THUMBNAIL
from .config import CLIENT_SECRETS, TOKEN_PICKLE
from .config import YT_COOKIES_FILE, MAX_PARALLEL_GENERATIONS, DUP_REGEN_RETRIES, TEMP_DIR_MAX_AGE_MINUTES
from .utils import ensure_gitignore_entries, load_urls_json
from .sources import scrape_one_from_pinterest
from .audio import download_random_song_from_playlist, extract_random_audio_clip, get_song_title
from .video import convert_to_tiktok_format, generate_thumbnail, get_video_metadata
from .metadata import generate_metadata_from_source
from .uploaders import youtube_authenticate, youtube_upload_short, instagram_upload
from .uploaders import x_upload
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

def cleanup_old_generated_files(max_age_days: int = 7, dry_run: bool = False):
    base = Path('.')
    now = datetime.datetime.utcnow()
    max_age = datetime.timedelta(days=max_age_days)
    
    patterns = {
        'videos': 'tiktok_video_*.mp4',
        'thumbnails': 'thumbnail_*.jpg'
    }
    
    stats = {
        'videos_removed': 0,
        'videos_size': 0,
        'thumbnails_removed': 0,
        'thumbnails_size': 0,
        'videos_kept': 0,
        'thumbnails_kept': 0
    }
    
    try:
        for category, pattern in patterns.items():
            for item in base.glob(pattern):
                try:
                    if not item.is_file():
                        continue
                    
                    mtime = datetime.datetime.utcfromtimestamp(item.stat().st_mtime)
                    age = now - mtime
                    file_size = item.stat().st_size
                    
                    if age > max_age:
                        if dry_run:
                            print(f"[DRY RUN] Would delete: {item.name} (age: {age.days} days, size: {file_size} bytes)", flush=True)
                        else:
                            try:
                                item.unlink()
                                if category == 'videos':
                                    stats['videos_removed'] += 1
                                    stats['videos_size'] += file_size
                                else:
                                    stats['thumbnails_removed'] += 1
                                    stats['thumbnails_size'] += file_size
                            except Exception as e:
                                logging.error(f"Failed to delete {item.name}: {e}")
                    else:
                        if category == 'videos':
                            stats['videos_kept'] += 1
                        else:
                            stats['thumbnails_kept'] += 1
                except Exception as e:
                    logging.error(f"Error processing file {item}: {e}")
    except Exception as e:
        logging.error(f"Error during cleanup: {e}")
    
    return stats

def generate_meme_video(
    pinterest_urls: list[str],
    music_playlists: list[str],
    pin_num: int = 10000,
    audio_duration: int = 10,
    progress: Optional[Callable[[str], None]] = None,
    seed: int | None = None,
    variant_group: int | None = None,
    reddit_sources: list[str] | None = None,
    twitter_sources: list[str] | None = None,
):
    notify = (lambda msg: progress(msg) if callable(progress) else None)
    set_phase('init')
    
    try:
        unique_id = datetime.datetime.now().strftime('%Y%m%d_%H%M%S_') + os.urandom(3).hex()
        pins_dir = f"{DEFAULT_PINS_DIR}_{unique_id}"
        audio_dir = f"{DEFAULT_AUDIO_DIR}_{unique_id}"
        Path(pins_dir).mkdir(parents=True, exist_ok=True)
        Path(audio_dir).mkdir(parents=True, exist_ok=True)
    except Exception as e:
        notify(f"❌ Ошибка создания временных директорий: {e}")
        logging.error(f"Ошибка создания временных директорий: {e}", exc_info=True)
        return GenerationResult(None, None, None, None, None)
    
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
        notify("🎯 Пробую Reddit…")
        try:
            from .sources import fetch_one_from_reddit
            path = fetch_one_from_reddit(reddit_sources, output_dir=pins_dir)
            if path:
                base = os.path.basename(path)
                parts = base.split('_')
                sr = parts[1] if len(parts) >= 2 else 'reddit'
                notify("🖼️ Получено изображение с Reddit")
                return path, f"reddit:{sr}"
        except Exception as e:
            print(f"Reddit provider error: {e}", flush=True)
        return None, None

    def _twitter_provider():
        if not twitter_sources:
            return None, None
        notify("🐦 Пробую Twitter/X…")
        try:
            from .sources import fetch_one_from_twitter
            path = fetch_one_from_twitter(twitter_sources, output_dir=pins_dir)
            if path:
                base = os.path.basename(path)
                parts = base.split('_')
                username = parts[1] if len(parts) >= 2 else 'twitter'
                notify("🖼️ Получено изображение из Twitter")
                return path, f"twitter:@{username}"
        except Exception as e:
            print(f"Twitter provider error: {e}", flush=True)
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
    if twitter_sources:
        sources_candidates.append(("twitter", _twitter_provider))
    sources_candidates.append(("meme_api", _meme_api_provider))

    
    random.shuffle(sources_candidates)
    downloaded_path = None
    chosen_pinterest = None
    tried = []
    
    try:
        for name, provider in sources_candidates:
            print(f"Trying source provider: {name}", flush=True)
            set_phase(f'source:{name}')
            try:
                path, src = provider()
                tried.append(name)
                if path:
                    downloaded_path = path
                    chosen_pinterest = src
                    print(f"Source {name} succeeded with file {path}", flush=True)
                    break
                else:
                    print(f"Source {name} returned no result, continuing", flush=True)
            except Exception as e:
                logging.error(f"Ошибка при получении контента из источника {name}: {e}", exc_info=True)
                tried.append(f"{name}(error)")
                print(f"Source {name} failed with error: {e}, continuing", flush=True)
        print(f"Tried sources order: {tried}", flush=True)
    except Exception as e:
        logging.error(f"Критическая ошибка в цикле источников: {e}", exc_info=True)
        notify(f"❌ Критическая ошибка при поиске контента: {e}")
        return GenerationResult(None, None, None, None, None)
    
    print(f"Final downloaded_path: {downloaded_path}", flush=True)
    if downloaded_path:
        print(f"Downloaded file exists: {os.path.exists(downloaded_path)}", flush=True)
        if os.path.exists(downloaded_path):
            print(f"File size: {os.path.getsize(downloaded_path)} bytes", flush=True)
    
    if not downloaded_path:
        notify("❌ Не удалось получить исходный мем")
        return GenerationResult(None, None, chosen_pinterest, None, None)
    
    
    audio_clip_path = None
    original_audio_path = None
    audio_title = None
    chosen_music = random.choice(music_playlists) if music_playlists else None
    print(f"Selected music playlist: {chosen_music}", flush=True)
    
    cookies_path = os.getenv('YT_COOKIES_FILE') or 'youtube_cookies.txt'
    try:
        cookies_available = os.path.isfile(cookies_path)
    except Exception:
        cookies_available = False
    if not cookies_available:
        notify("⚠️ ВНИМАНИЕ: youtube_cookies.txt не найден!")
        notify("YouTube может заблокировать загрузку. Загрузите cookies через /uploadytcookies")
    
    audio_attempts = 0
    max_audio_attempts = 3
    audio_success = False
    last_audio_error = None
    
    if chosen_music:
        while audio_attempts < max_audio_attempts and not audio_success:
            audio_attempts += 1
            try:
                set_phase('audio_download')
                if audio_attempts == 1:
                    notify("🎵 Скачиваю трек из плейлиста…")
                else:
                    notify(f"🔄 Повторная попытка загрузки аудио ({audio_attempts}/{max_audio_attempts})…")
                
                audio_path = download_random_song_from_playlist(chosen_music, output_dir=audio_dir)
                print(f"Downloaded audio path: {audio_path}", flush=True)
                
                if audio_path:
                    original_audio_path = audio_path
                    audio_title = get_song_title(audio_path)
                    notify("✂️ Вырезаю аудио-клип нужной длительности…")
                    set_phase('audio_clip')
                    audio_clip_path = extract_random_audio_clip(audio_path, clip_duration=audio_duration)
                    print(f"Extracted audio clip path: {audio_clip_path}", flush=True)
                    
                    if audio_clip_path and os.path.exists(audio_clip_path):
                        audio_success = True
                        notify(f"✅ Аудио успешно добавлено: {audio_title or 'трек'}")
                    
                    if audio_path != audio_clip_path and os.path.exists(audio_path):
                        try:
                            os.remove(audio_path)
                        except Exception:
                            pass
                    
                    if audio_success:
                        break
            except Exception as e:
                last_audio_error = str(e)
                error_str = str(e).lower()
                logging.error(f"Ошибка при обработке аудио (попытка {audio_attempts}/{max_audio_attempts}): {e}", exc_info=True)
                
                if '403' in error_str or 'forbidden' in error_str:
                    if not cookies_available:
                        notify("❌ YouTube блокирует загрузку (403)")
                        notify("📋 РЕШЕНИЕ: Загрузите youtube_cookies.txt через /uploadytcookies")
                        notify("Инструкция: используйте расширение 'Get cookies.txt LOCALLY' для Chrome/Firefox")
                        break
                    else:
                        notify(f"⚠️ Ошибка 403 даже с cookies (попытка {audio_attempts}/{max_audio_attempts})")
                        if audio_attempts < max_audio_attempts:
                            import time
                            time.sleep(3)
                elif audio_attempts < max_audio_attempts:
                    notify(f"⚠️ Ошибка: {e}")
                    import time
                    time.sleep(2)
        
        if not audio_success:
            notify(f"❌ Не удалось загрузить аудио после {max_audio_attempts} попыток")
            if '403' in str(last_audio_error).lower():
                notify("⚠️ Последняя ошибка: YouTube блокирует доступ (403)")
                notify("📋 Решение: /uploadytcookies для загрузки актуальных cookies")
            else:
                notify(f"⚠️ Последняя ошибка: {last_audio_error}")
            notify("⚠️ Видео будет создано БЕЗ ЗВУКА")
            logging.error(f"Финальная ошибка при загрузке аудио: {last_audio_error}")
    else:
        notify("⚠️ Нет плейлистов для музыки, видео будет без звука")
    unique_suffix = datetime.datetime.now().strftime('%Y%m%d_%H%M%S_') + os.urandom(3).hex()
    output_path = f"tiktok_video_{unique_suffix}.mp4"
    
    try:
        set_phase('video_convert')
        notify("🎬 Конвертирую видео в формат TikTok…")
        print(f"Starting video conversion with downloaded_path: {downloaded_path}, output_path: {output_path}", flush=True)
        result_path = convert_to_tiktok_format(downloaded_path, output_path, is_youtube=False, audio_path=audio_clip_path, seed=seed, variant_group=variant_group)
        print(f"Video conversion result: {result_path}", flush=True)
        if not result_path or not os.path.exists(result_path):
            print("Video conversion failed", flush=True)
            notify("❌ Ошибка при конвертации видео")
            return GenerationResult(None, None, chosen_pinterest, None, audio_title)
        
        try:
            metadata = get_video_metadata(result_path)
            if metadata:
                has_audio = metadata.get('has_audio', False)
                duration = metadata.get('duration', 0)
                print(f"Video metadata: has_audio={has_audio}, duration={duration}s", flush=True)
                
                if audio_clip_path and not has_audio:
                    notify("⚠️ ВНИМАНИЕ: Видео создано, но аудио НЕ ДОБАВЛЕНО")
                    logging.warning(f"Audio was expected but not found in final video. audio_clip_path={audio_clip_path}")
                elif has_audio and audio_title:
                    notify(f"✅ Видео с аудио готово ({duration:.1f}с)")
                elif has_audio:
                    notify(f"✅ Видео с аудио готово ({duration:.1f}с)")
                else:
                    notify(f"ℹ️ Видео без аудио готово ({duration:.1f}с)")
        except Exception as meta_err:
            logging.warning(f"Не удалось проверить метаданные видео: {meta_err}")
    except Exception as e:
        logging.error(f"Критическая ошибка при конвертации видео: {e}", exc_info=True)
        notify(f"❌ Критическая ошибка при конвертации видео: {e}")
        return GenerationResult(None, None, chosen_pinterest, None, audio_title)
    
    thumbnail_path = f"thumbnail_{unique_suffix}.jpg"
    try:
        set_phase('thumbnail')
        notify("🖼️ Генерирую миниатюру…")
        print(f"Generating thumbnail for: {output_path}", flush=True)
        thumb_result = generate_thumbnail(output_path, thumbnail_path)
        print(f"Thumbnail generation result: {thumb_result}", flush=True)
        if not thumb_result or not os.path.exists(thumb_result):
            print("Thumbnail generation failed", flush=True)
            notify("❌ Не удалось создать миниатюру")
            return GenerationResult(None, None, chosen_pinterest, None, audio_title)
    except Exception as e:
        logging.error(f"Критическая ошибка при создании миниатюры: {e}", exc_info=True)
        notify(f"❌ Критическая ошибка при создании миниатюры: {e}")
        return GenerationResult(None, None, chosen_pinterest, None, audio_title)
    
    try:
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
    except Exception as e:
        logging.error(f"Ошибка при очистке временных файлов: {e}", exc_info=True) 
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
    
    # Default to all socials if none specified (TikTok удалён)
    if socials is None:
        socials = ['youtube', 'instagram', 'x']
    
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

    # TikTok загрузка полностью отключена
    tiktok_link = None

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
    # После завершения всех загрузок можно безопасно удалить локальные файлы
    try:
        if not dry_run:
            for path in [video_path, thumbnail_path]:
                try:
                    if path and os.path.exists(path):
                        os.remove(path)
                except Exception:
                    # Ошибки удаления не должны ломать основной флоу
                    pass
    except Exception:
        # Любые неожиданные ошибки при очистке логируем, но не пробрасываем выше
        logging.error("Ошибка при удалении опубликованных файлов", exc_info=True)

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
    
    audio_attempts = 0
    max_audio_attempts = 3
    audio_success = False
    last_audio_error = None
    audio_path = None
    audio_clip_path = None
    
    try:
        while audio_attempts < max_audio_attempts and not audio_success:
            audio_attempts += 1
            try:
                chosen_music = random.choice(music_playlists)
                
                if audio_attempts == 1:
                    notify("🎵 Скачиваю новый трек из плейлиста…")
                else:
                    notify(f"🔄 Повторная попытка загрузки аудио ({audio_attempts}/{max_audio_attempts})…")
                
                set_phase('audio_download')
                
                audio_path = download_random_song_from_playlist(chosen_music, output_dir=audio_dir)
                
                notify("✂️ Вырезаю аудио-клип нужной длительности…")
                set_phase('audio_clip')
                audio_clip_path = extract_random_audio_clip(audio_path, clip_duration=audio_duration)
                
                if audio_clip_path and os.path.exists(audio_clip_path):
                    audio_success = True
                    break
            except Exception as e:
                last_audio_error = str(e)
                logging.error(f"Ошибка при обработке аудио (попытка {audio_attempts}/{max_audio_attempts}): {e}", exc_info=True)
                if audio_attempts < max_audio_attempts:
                    notify(f"⚠️ Ошибка: {e}")
                    import time
                    time.sleep(2)
        
        if not audio_success:
            notify(f"❌ Не удалось загрузить аудио после {max_audio_attempts} попыток")
            notify(f"⚠️ Последняя ошибка: {last_audio_error}")
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
        audio_title = get_song_title(audio_path) if audio_path else None
        
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
