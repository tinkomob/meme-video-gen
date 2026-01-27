import asyncio
import os
import json
import time
import logging
import threading
import gc
from typing import Optional
from datetime import datetime, timedelta
try:
    from zoneinfo import ZoneInfo
except Exception:
    ZoneInfo = None
from io import BytesIO

from dotenv import load_dotenv
from app.config import TELEGRAM_BOT_TOKEN, DEFAULT_THUMBNAIL, HISTORY_FILE
from app.service import generate_meme_video, deploy_to_socials, cleanup_old_temp_dirs, cleanup_old_generated_files, replace_audio_in_video, process_uploaded_video_with_audio
from app.logger import setup_error_logging
from app.utils import load_urls_json, replace_file_from_bytes, clear_video_history, read_small_file
from app.history import add_video_history_item, load_video_history, save_video_history
from app.config import CLIENT_SECRETS, TOKEN_PICKLE, YT_COOKIES_FILE
from app.state import set_last_chat_id, get_last_chat_id, set_next_run_iso, get_next_run_iso, set_daily_schedule_iso, get_daily_schedule_iso, set_selected_chat_id, get_selected_chat_id
from app.config import DAILY_GENERATIONS, DUP_REGEN_RETRIES
from app.video import get_video_metadata
from app.crash_handler import save_generation_state, load_last_crash_info, clear_crash_info, get_last_uncaught_exception, get_recent_phases, log_resource_usage, get_memory_info
try:
    import telegram.error as tgerr
except Exception:
    tgerr = None
try:
    import httpx
except Exception:
    httpx = None

def _tz_tomsk():
    try:
        if ZoneInfo is not None:
            return ZoneInfo("Asia/Tomsk")
    except Exception:
        pass
    try:
        from datetime import timezone
        offset = 7
        return timezone(timedelta(hours=offset))
    except Exception:
        return None

try:
    from telegram import InlineKeyboardButton, InlineKeyboardMarkup
except Exception:
    InlineKeyboardButton = None
    InlineKeyboardMarkup = None

load_dotenv()

setup_error_logging('errors.log')

# Suppress spammy 'Empty response received.' lines coming from third-party libs (moviepy/pytube/etc.) by collapsing repeats.
try:
    import sys
    _spam_phrase = "Empty response received."
    _orig_write = sys.stdout.write
    _lock = threading.Lock()
    _last_line = {"text": None, "count": 0, "last_flush": 0.0}
    _flush_interval = 5.0  # seconds
    def _flush_summary(force=False):
        now = time.time()
        if _last_line["count"] > 1 and (force or now - _last_line["last_flush"] >= _flush_interval):
            try:
                from app.debug import get_phase
                phase = get_phase()
            except Exception:
                phase = 'unknown'
            _orig_write(f"(suppressed {_last_line['count']-1} repeats of '{_spam_phrase}' phase={phase})\n")
            _last_line["count"] = 0
            _last_line["last_flush"] = now
    def _wrapped_write(data: str) -> int:
        try:
            if _spam_phrase in data:
                with _lock:
                    if _last_line["text"] == _spam_phrase:
                        _last_line["count"] += 1
                    else:
                        _flush_summary(force=True)
                        _last_line["text"] = _spam_phrase
                        _last_line["count"] = 1
                    _last_line["last_flush"] = time.time()
                return len(data)
            else:
                with _lock:
                    _flush_summary(force=True)
                return _orig_write(data)
        except Exception:
            return _orig_write(data)
    sys.stdout.write = _wrapped_write
except Exception:
    pass

# Global crash instrumentation
try:
    import sys, traceback
    def _global_excepthook(exctype, value, tb):
        try:
            with open('crash.log', 'a', encoding='utf-8') as f:
                f.write('\n=== Uncaught Exception ===\n')
                traceback.print_exception(exctype, value, tb, file=f)
        except Exception:
            pass
        sys.__excepthook__(exctype, value, tb)
    sys.excepthook = _global_excepthook
except Exception:
    pass

# Heartbeat writer to detect external restarts
try:
    def _heartbeat():
        while True:
            try:
                with open('heartbeat.log', 'w', encoding='utf-8') as f:
                    f.write(str(int(time.time())))
            except Exception:
                pass
            time.sleep(30)
    th = threading.Thread(target=_heartbeat, daemon=True)
    th.start()
except Exception:
    pass

logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

for _name in (
    "httpx",
    "httpcore",
    "urllib3",
    "h11",
    "telegram",
    "telegram.ext",
    "telegram.request",
    "telegram._network",
    "telegram.bot",
):
    try:
        logging.getLogger(_name).setLevel(logging.WARNING)
        logging.getLogger(_name).propagate = False
    except Exception:
        pass


def _resolve_notify_chat_id() -> Optional[int]:
    try:
        cid = get_selected_chat_id() or get_last_chat_id()
        if cid:
            return cid
    except Exception:
        pass
    env_cid = os.getenv("TELEGRAM_NOTIFY_CHAT_ID") or os.getenv("ADMIN_CHAT_ID")
    if env_cid:
        try:
            return int(env_cid)
        except Exception:
            return None
    return None

DEFAULT_PINTEREST_JSON = "pinterest_urls.json"
DEFAULT_PLAYLISTS_JSON = "music_playlists.json"
DEFAULT_REDDIT_JSON = "reddit_sources.json"
DEFAULT_TWITTER_JSON = "twitter_urls.json"
ALL_SOCIALS = ["youtube", "instagram", "x"]


HELP_TEXT = (
    "Команды:\n"
    "/start — приветствие\n"
    "/help — помощь\n"
    "/status — статус генерации и использование памяти\n"
    "/generate — генерация мемов. Форматы: /generate N (N видео), /generate <pin_num> <audio_duration> [count=M]. Примеры: /generate 3; /generate 80 12 count=2\n"
    "  Поддерживает источники: Pinterest (pinterest_urls.json), Reddit (reddit_sources.json), Twitter (twitter_urls.json), музыка (music_playlists.json)\n"
    "  После генерации доступны кнопки: Опубликовать, Выбрать платформы, Сменить трек, Сгенерировать заново\n"
    "\n"
    "📤 ЗАГРУЗКА СВОЕГО ВИДЕО:\n"
    "1. Отправьте видео в чат\n"
    "2. Выберите способ добавления аудио:\n"
    "   • 🎲 Случайный трек из плейлистов\n"
    "   • 📤 Загрузить свой аудио файл (MP3/WAV)\n"
    "   • 🔍 Поиск трека в плейлистах\n"
    "3. Бот обработает видео и добавит выбранный аудио\n"
    "4. Доступны те же опции: публикация, смена трека\n"
    "\n"
    "/deploy — опубликовать последнее видео (опционально: соцсети, приватность, dry)\n"
    "/dryrun — показать/изменить режим публикации (on/off)\n"
    "/checkfiles — проверить cookies.txt, youtube_cookies.txt, client_secrets.json и token.pickle\n"
    "/pinterestcheck — проверить состояние Pinterest и режим резервного источника контента\n"
    "/history — последние публикации\n"
    "/uploadytcookies — загрузить youtube_cookies.txt (YouTube) как документ\n"
    "/uploadclient — загрузить client_secrets.json как документ\n"
    "/uploadtoken — загрузить token.pickle как документ\n"
    "/clearhistory — очистить video_history.json\n"
    "/scheduleinfo — показать расписание всех генераций на сегодня\n"
    "/runscheduled — немедленно выполнить ближайшую запланированную генерацию\n"
    "/setnext — изменить время запланированной генерации: /setnext <index> <время|сдвиг> (пример: /setnext 2 22:10, /setnext 1 +30m)\n"
    "/chatid — показать и сохранить текущий chat id\n"
    "/cleanup — очистить старые файлы (опционально: days=N, dry для тестового режима). Пример: /cleanup days=14 dry\n"
    "  Удаляет временные директории, tiktok_video_*.mp4 и thumbnail_*.jpg старше N дней (по умолчанию 7)\n"
    "/rebuildschedule — пересоздать расписание генераций на сегодня\n"
    "\n"
    "Кнопка 'Сменить трек' заменяет аудиотрек в уже созданном видео на случайный из плейлистов (макс. 12 сек).\n"
)


BOT_DRY_RUN_DEFAULT = os.getenv("BOT_DRY_RUN", "false").lower() == "true"
PROGRESS_DEBOUNCE_MS = int(os.getenv("BOT_PROGRESS_DEBOUNCE_MS", "1000") or 1000)
PROGRESS_DEBOUNCE_SEC = max(0.2, PROGRESS_DEBOUNCE_MS / 1000)


def _add_msg_id(context, msg_id: int) -> None:
    lst = context.chat_data.setdefault("gen_msg_ids", [])
    if msg_id not in lst:
        lst.append(msg_id)


def _pop_all_msg_ids(context) -> list[int]:
    return context.chat_data.pop("gen_msg_ids", [])


async def _send_and_track_text(context, chat_id: int, text: str):
    try:
        m = await context.bot.send_message(chat_id=chat_id, text=text)
        if m and getattr(m, "message_id", None):
            _add_msg_id(context, m.message_id)
        return m
    except Exception:
        return None


async def _progress_init(context, chat_id: int, initial_text: str):
    try:
        msg = await context.bot.send_message(chat_id=chat_id, text=initial_text)
        if msg and getattr(msg, "message_id", None):
            context.chat_data["progress_msg_id"] = msg.message_id
            context.chat_data["progress_lines"] = [initial_text]
            context.chat_data["progress_last_edit"] = time.monotonic()
            context.chat_data["progress_flush_task"] = None
            _add_msg_id(context, msg.message_id)
        return msg
    except Exception:
        return None


async def _progress_edit(context, chat_id: int, full_text: str):
    try:
        pmid = context.chat_data.get("progress_msg_id")
        if pmid:
            return await context.bot.edit_message_text(chat_id=chat_id, message_id=pmid, text=full_text)
        else:
            return await _progress_init(context, chat_id, full_text)
    except Exception:
        return None


async def _progress_schedule_flush(context, chat_id: int, delay: float):
    try:
        async def runner():
            try:
                await asyncio.sleep(delay)
                lines = context.chat_data.get("progress_lines") or []
                await _progress_edit(context, chat_id, "\n".join(lines))
                context.chat_data["progress_last_edit"] = time.monotonic()
            finally:
                context.chat_data["progress_flush_task"] = None

        if context.chat_data.get("progress_flush_task") is None:
            context.chat_data["progress_flush_task"] = asyncio.create_task(runner())
    except Exception:
        pass


async def _progress_queue(context, chat_id: int, line: str, max_lines: int = 25):
    try:
        lines = context.chat_data.get("progress_lines") or []
        lines.append(line)
        if len(lines) > max_lines:
            lines = lines[-max_lines:]
        context.chat_data["progress_lines"] = lines
        last = context.chat_data.get("progress_last_edit") or 0.0
        now = time.monotonic()
        elapsed = now - last
        delay = PROGRESS_DEBOUNCE_SEC if elapsed < PROGRESS_DEBOUNCE_SEC else 0.0
        await _progress_schedule_flush(context, chat_id, delay)
    except Exception:
        pass


def _get_bot_dry_run(context) -> bool:
    val = context.application.bot_data.get("dry_run") if getattr(context, "application", None) else None
    if isinstance(val, bool):
        return val
    return BOT_DRY_RUN_DEFAULT

def _is_generation_locked(app) -> bool:
    return app.bot_data.get('generation_locked', False)

def _lock_generation(app) -> bool:
    if _is_generation_locked(app):
        return False
    app.bot_data['generation_locked'] = True
    app.bot_data['generation_lock_time'] = time.time()
    logging.info("🔒 Генерация заблокирована")
    return True

def _unlock_generation(app):
    was_locked = app.bot_data.get('generation_locked', False)
    app.bot_data['generation_locked'] = False
    app.bot_data.pop('generation_lock_time', None)
    if was_locked:
        logging.info("🔓 Генерация разблокирована")
        gc.collect()
        try:
            log_resource_usage("after_unlock")
        except Exception:
            pass


async def _send_video_slider(context, chat_id: int, results: list, generation_id: str) -> list:
    """Отправляет первое видео из списка со слайдер-кнопками и сохраняет данные для навигации.
    
    Args:
        context: Контекст бота
        chat_id: ID чата для отправки
        results: Список элементов истории видео
        generation_id: Уникальный ID генерации для отслеживания
    
    Returns:
        Список отправленных message_id
    """
    if not results:
        return []
    
    msg_ids = []
    
    # Отправляем заголовок
    try:
        header_text = f"✅ Готово! Создано {len(results)} видео.\nИспользуйте кнопки для навигации:"
        m_header = await context.bot.send_message(chat_id=chat_id, text=header_text)
        if m_header and getattr(m_header, 'message_id', None):
            msg_ids.append(m_header.message_id)
    except Exception as e:
        logging.error(f"Ошибка отправки заголовка слайдера: {e}")
    
    # Отправляем первое видео с навигацией
    current_idx = 0
    item = results[current_idx]
    vid = item.get('id')
    
    # Формируем caption с счётчиком
    caption_lines = [f"📹 Видео {current_idx + 1}/{len(results)}"]
    caption_lines.append(f"ID: #{vid}")
    caption_lines.append("")
    caption_lines.append(_format_video_info_from_history(item))
    caption = "\n".join(caption_lines)
    
    # Создаём кнопки навигации и действий
    kb = _build_slider_keyboard(generation_id, current_idx, len(results), vid)
    
    try:
        if item.get('video_path') and os.path.exists(item.get('video_path')):
            m_video = await context.bot.send_video(
                chat_id=chat_id,
                video=open(item.get('video_path'), 'rb'),
                caption=caption,
                reply_markup=kb
            )
            if m_video and getattr(m_video, 'message_id', None):
                msg_ids.append(m_video.message_id)
                video_msg_id = m_video.message_id
        else:
            m_video = await context.bot.send_message(
                chat_id=chat_id,
                text=caption,
                reply_markup=kb
            )
            if m_video and getattr(m_video, 'message_id', None):
                msg_ids.append(m_video.message_id)
                video_msg_id = m_video.message_id
    except Exception as e:
        logging.error(f"Ошибка отправки видео слайдера: {e}")
        return msg_ids
    
    # Сохраняем данные для навигации
    try:
        slider_data = context.application.bot_data.setdefault('video_sliders', {})
        slider_data[generation_id] = {
            'chat_id': chat_id,
            'results': [r.get('id') for r in results],  # Храним только ID
            'current_idx': current_idx,
            'msg_ids': msg_ids,
            'video_msg_id': video_msg_id
        }
    except Exception as e:
        logging.error(f"Ошибка сохранения данных слайдера: {e}")
    
    return msg_ids


def _build_slider_keyboard(generation_id: str, current_idx: int, total: int, video_id: str):
    """Создаёт клавиатуру со слайдер-кнопками и кнопками действий."""
    if not InlineKeyboardButton or not InlineKeyboardMarkup:
        return None
    
    buttons = []
    
    # Кнопки навигации
    nav_row = []
    if current_idx > 0:
        nav_row.append(InlineKeyboardButton("◀️ Назад", callback_data=f"slider_prev:{generation_id}"))
    else:
        nav_row.append(InlineKeyboardButton("◀️", callback_data=f"slider_noop"))
    
    nav_row.append(InlineKeyboardButton(f"{current_idx + 1}/{total}", callback_data=f"slider_noop"))
    
    if current_idx < total - 1:
        nav_row.append(InlineKeyboardButton("Вперёд ▶️", callback_data=f"slider_next:{generation_id}"))
    else:
        nav_row.append(InlineKeyboardButton("▶️", callback_data=f"slider_noop"))
    
    buttons.append(nav_row)
    
    # Кнопки действий для текущего видео
    buttons.append([
        InlineKeyboardButton(f"Опубликовать #{video_id}", callback_data=f"publish:{video_id}")
    ])
    buttons.append([
        InlineKeyboardButton(f"Выбрать платформы #{video_id}", callback_data=f"choose:{video_id}")
    ])
    buttons.append([
        InlineKeyboardButton(f"Сменить трек #{video_id}", callback_data=f"changeaudio:{video_id}")
    ])
    
    # Кнопка регенерации всей пачки
    buttons.append([
        InlineKeyboardButton("🔄 Сгенерировать заново", callback_data=f"slider_regen:{generation_id}")
    ])
    
    return InlineKeyboardMarkup(buttons)


async def _update_slider_video(context, generation_id: str, new_idx: int):
    """Обновляет видео в слайдере при навигации."""
    try:
        slider_data = context.application.bot_data.get('video_sliders', {}).get(generation_id)
        if not slider_data:
            return False
        
        chat_id = slider_data['chat_id']
        video_msg_id = slider_data['video_msg_id']
        result_ids = slider_data['results']
        
        if new_idx < 0 or new_idx >= len(result_ids):
            return False
        
        # Загружаем историю и находим нужный элемент
        hist = load_video_history()
        current_id = result_ids[new_idx]
        item = next((it for it in hist if it.get('id') == current_id), None)
        
        if not item:
            return False
        
        # Обновляем индекс
        slider_data['current_idx'] = new_idx
        
        # Формируем новый caption
        vid = item.get('id')
        caption_lines = [f"📹 Видео {new_idx + 1}/{len(result_ids)}"]
        caption_lines.append(f"ID: #{vid}")
        caption_lines.append("")
        caption_lines.append(_format_video_info_from_history(item))
        caption = "\n".join(caption_lines)
        
        # Создаём новую клавиатуру
        kb = _build_slider_keyboard(generation_id, new_idx, len(result_ids), vid)
        
        # Удаляем старое сообщение с видео
        try:
            await context.bot.delete_message(chat_id=chat_id, message_id=video_msg_id)
        except Exception as e:
            logging.warning(f"Не удалось удалить старое видео: {e}")
        
        # Отправляем новое видео
        try:
            if item.get('video_path') and os.path.exists(item.get('video_path')):
                m_video = await context.bot.send_video(
                    chat_id=chat_id,
                    video=open(item.get('video_path'), 'rb'),
                    caption=caption,
                    reply_markup=kb
                )
            else:
                m_video = await context.bot.send_message(
                    chat_id=chat_id,
                    text=caption,
                    reply_markup=kb
                )
            
            if m_video and getattr(m_video, 'message_id', None):
                slider_data['video_msg_id'] = m_video.message_id
                return True
        except Exception as e:
            logging.error(f"Ошибка отправки нового видео в слайдере: {e}")
            return False
    
    except Exception as e:
        logging.error(f"Ошибка обновления слайдера: {e}")
        return False


def _format_video_info_from_history(item: dict) -> str:
    """Форматирует информацию о видео из истории для отправки пользователю"""
    lines = []
    
    # Основная информация
    lines.append("📹 Сгенерированное видео")
    
    # Информация об источнике
    source_url = item.get('source_url')
    if source_url:
        lines.append(f"📎 Источник: {source_url}")
    else:
        lines.append("📎 Источник: неизвестен")
    
    return "\n".join(lines)


def _format_video_info(result) -> str:
    """Форматирует информацию о сгенерированном видео для отправки пользователю"""
    lines = []
    
    # Основная информация
    lines.append("✅ Видео готово!")
    
    # Информация об источнике
    if result.source_url:
        lines.append(f"📎 Источник: {result.source_url}")
    else:
        lines.append("📎 Источник: неизвестен")
    
    # Информация об аудиотреке
    if result.audio_title:
        lines.append(f"🎵 Музыка: {result.audio_title}")
    
    return "\n".join(lines)


def parse_int(value: Optional[str], default: int) -> int:
    try:
        if value is None:
            return default
        return int(value)
    except Exception:
        return default


async def cmd_start(update, context):
    try:
        set_last_chat_id(update.effective_chat.id)
    except Exception:
        pass
    await update.message.reply_text("Привет! Я бот для генерации мем-видео. Наберите /help для списка команд.")


async def cmd_help(update, context):
    await update.message.reply_text(HELP_TEXT)

async def cmd_status(update, context):
    try:
        set_last_chat_id(update.effective_chat.id)
    except Exception:
        pass
    
    lines = ["📊 Статус системы\n"]
    
    try:
        mem_info = get_memory_info()
        lines.append(f"💾 Память:")
        lines.append(f"  RSS: {mem_info.get('rss_mb', 0)} MB")
        lines.append(f"  VMS: {mem_info.get('vms_mb', 0)} MB")
        lines.append(f"  Использование: {mem_info.get('percent', 0)}%")
        lines.append(f"  Потоки: {mem_info.get('threads', 0)}\n")
    except Exception as e:
        lines.append(f"⚠️ Ошибка получения информации о памяти: {e}\n")
    
    is_locked = _is_generation_locked(context.application)
    if is_locked:
        lock_time = context.application.bot_data.get('generation_lock_time', 0)
        elapsed = int(time.time() - lock_time)
        lines.append(f"🔒 Генерация: ЗАБЛОКИРОВАНА")
        lines.append(f"  Запущена {elapsed} сек. назад\n")
    else:
        lines.append(f"🔓 Генерация: СВОБОДНА\n")
    
    try:
        crash_info = load_last_crash_info()
        if crash_info:
            lines.append(f"⚠️ Последняя проблема:")
            lines.append(f"  Этап: {crash_info.get('state', 'неизвестно')}")
            lines.append(f"  Ошибка: {crash_info.get('error', 'неизвестно')[:100]}")
            lines.append(f"  Время: {crash_info.get('time', 'неизвестно')}\n")
    except Exception:
        pass
    
    try:
        tz = _tz_tomsk()
        now = datetime.now(tz)
        sched = get_daily_schedule_iso()
        today = [s for s in sched if datetime.fromisoformat(s).date() == now.date()]
        future = [s for s in today if datetime.fromisoformat(s) > now]
        if future:
            next_dt = datetime.fromisoformat(min(future))
            delta_sec = int((next_dt - now).total_seconds())
            delta_min = delta_sec // 60
            lines.append(f"⏰ Следующая генерация через {delta_min} мин. ({next_dt.strftime('%H:%M:%S')})")
    except Exception:
        pass
    
    await update.message.reply_text("\n".join(lines))



async def cmd_generate(update, context):
    try:
        set_last_chat_id(update.effective_chat.id)
    except Exception:
        pass
    args = context.args or []
    count = 1
    pin_num = 10000
    audio_duration = 10
    if args:
        if len(args) == 1:
            v = parse_int(args[0], 1)
            if v > 1:
                count = min(v, 20)
            else:
                pin_num = v
        else:
            pin_num = parse_int(args[0], 10000)
            audio_duration = parse_int(args[1], 10) if len(args) >= 2 else 10
            for a in args[2:]:
                if a.startswith("count="):
                    try:
                        count = min(int(a.split("=",1)[1]), 20)
                    except Exception:
                        pass
    pinterest_urls = load_urls_json(DEFAULT_PINTEREST_JSON, [])
    music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
    reddit_sources = load_urls_json(DEFAULT_REDDIT_JSON, [])
    twitter_sources = load_urls_json(DEFAULT_TWITTER_JSON, [])
    if not pinterest_urls and not music_playlists and not reddit_sources and not twitter_sources:
        await update.message.reply_text("Нет источников. Добавьте ссылки в pinterest_urls.json, music_playlists.json, reddit_sources.json или twitter_urls.json")
        return
    if _is_generation_locked(context.application):
        lock_time = context.application.bot_data.get('generation_lock_time', 0)
        elapsed = int(time.time() - lock_time)
        await update.message.reply_text(f"⚠️ Генерация уже выполняется (запущена {elapsed} сек. назад). Пожалуйста, дождитесь завершения.")
        return
    
    if count > 1:
        await update.message.reply_text(f"Запускаю генерацию {count} мемов последовательно… Это может занять несколько минут.")
        async def background_batch(chat_id: int, batch_count: int):
            if not _lock_generation(context.application):
                try:
                    await context.bot.send_message(chat_id=chat_id, text="⚠️ Генерация уже выполняется. Пожалуйста, дождитесь завершения.")
                except Exception:
                    pass
                return
            
            try:
                log_resource_usage("batch_start")
            except Exception:
                pass
            
            start_ts = time.monotonic()
            async def gen_one(i: int):
                attempts = 0
                last_res = None
                while attempts < 3:
                    attempts += 1
                    def run_generation():
                        try:
                            seed = int.from_bytes(os.urandom(4), 'big') ^ (i + attempts * 7919)
                            return generate_meme_video(
                                pinterest_urls=pinterest_urls,
                                music_playlists=music_playlists,
                                pin_num=pin_num,
                                audio_duration=audio_duration,
                                progress=None,
                                seed=seed,
                                variant_group=i % 5,
                                reddit_sources=reddit_sources,
                                twitter_sources=twitter_sources,
                            )
                        except Exception as e:
                            logging.warning(f"Batch gen #{i} attempt {attempts} failed: {e}")
                            return None
                    try:
                        last_res = await asyncio.to_thread(run_generation)
                    except Exception as e:
                        logging.error(f"Background thread exception gen #{i} attempt {attempts}: {e}")
                        last_res = None
                    if last_res and getattr(last_res, 'video_path', None):
                        break
                    await asyncio.sleep(0.5 * attempts)
                return (i, last_res)
            try:
                gathered = []
                for i in range(batch_count):
                    result = await gen_one(i)
                    gathered.append(result)
                result_map = {idx_res: val for idx_res, val in gathered}
                success = 0
                fail = 0
                success_results = []
                
                for idx in range(batch_count):
                    result = result_map.get(idx)
                    if not result or not result.video_path:
                        fail += 1
                        try:
                            await context.bot.send_message(chat_id=chat_id, text=f"[{idx+1}/{batch_count}] ❌ Не удалось создать видео.")
                        except Exception:
                            pass
                        continue
                    success += 1
                    new_item = add_video_history_item(result.video_path, result.thumbnail_path, result.source_url, result.audio_path)
                    success_results.append(new_item)
                
                # Отправляем результаты через слайдер
                if success_results:
                    generation_id = os.urandom(4).hex()
                    try:
                        await _send_video_slider(context, chat_id, success_results, generation_id)
                    except Exception as e:
                        logging.error(f"Ошибка отправки слайдера: {e}")
                        # Fallback: отправляем как раньше
                        for item in success_results:
                            try:
                                caption = _format_video_info_from_history(item)
                                kb = None
                                if InlineKeyboardButton and InlineKeyboardMarkup:
                                    kb = InlineKeyboardMarkup([
                                        [InlineKeyboardButton("Опубликовать", callback_data=f"publish:{item['id']}")],
                                        [InlineKeyboardButton("Выбрать платформы", callback_data=f"choose:{item['id']}")],
                                        [InlineKeyboardButton("Сменить трек", callback_data=f"changeaudio:{item['id']}")],
                                    ])
                                await context.bot.send_video(chat_id=chat_id, video=open(item['video_path'], "rb"), caption=caption, reply_markup=kb)
                            except Exception:
                                pass
                
                elapsed = time.monotonic() - start_ts
                mins = int(elapsed // 60)
                secs = int(elapsed % 60)
                summary = f"Готово. Успех: {success}, ошибки: {fail}. Время: {mins}м {secs}с." if mins else f"Готово. Успех: {success}, ошибки: {fail}. Время: {secs}с."
                if fail > 0:
                    try:
                        await context.bot.send_message(chat_id=chat_id, text=summary)
                    except Exception:
                        pass
            finally:
                _unlock_generation(context.application)
                try:
                    log_resource_usage("batch_end")
                except Exception:
                    pass
        asyncio.create_task(background_batch(update.effective_chat.id, count))
        return
    
    if not _lock_generation(context.application):
        lock_time = context.application.bot_data.get('generation_lock_time', 0)
        elapsed = int(time.time() - lock_time)
        await update.message.reply_text(f"⚠️ Генерация уже выполняется (запущена {elapsed} сек. назад). Пожалуйста, дождитесь завершения.")
        return
    
    try:
        log_resource_usage("single_gen_start")
    except Exception:
        pass
    
    context.chat_data["gen_msg_ids"] = []
    context.chat_data.pop("progress_msg_id", None)
    context.chat_data.pop("progress_lines", None)
    header = f"Запускаю генерацию... pins={pin_num}, audio={audio_duration}s"
    await _progress_init(context, update.effective_chat.id, header)
    loop = asyncio.get_running_loop()
    def run_generation():
        def progress(msg: str):
            asyncio.run_coroutine_threadsafe(_progress_queue(context, update.effective_chat.id, msg), loop)
        return generate_meme_video(
            pinterest_urls=pinterest_urls,
            music_playlists=music_playlists,
            pin_num=pin_num,
            audio_duration=audio_duration,
            progress=progress,
            reddit_sources=reddit_sources,
            twitter_sources=twitter_sources,
        )
    result = await asyncio.to_thread(run_generation)
    
    _unlock_generation(context.application)
    try:
        log_resource_usage("single_gen_end")
    except Exception:
        pass
    
    if not result or not result.video_path:
        await update.message.reply_text("Не удалось создать видео.")
        return
    new_item = add_video_history_item(result.video_path, result.thumbnail_path, result.source_url, result.audio_path)
    caption = _format_video_info(result)
    kb = None
    if InlineKeyboardButton and InlineKeyboardMarkup:
        kb = InlineKeyboardMarkup([
            [InlineKeyboardButton("Опубликовать", callback_data=f"publish:{new_item['id']}")],
            [InlineKeyboardButton("Выбрать платформы", callback_data=f"choose:{new_item['id']}")],
            [InlineKeyboardButton("Сменить трек", callback_data=f"changeaudio:{new_item['id']}")],
            [InlineKeyboardButton("Сгенерировать заново", callback_data=f"regenerate:{new_item['id']}")],
        ])
    try:
        m = await update.message.reply_video(video=open(result.video_path, "rb"), caption=caption, reply_markup=kb)
        try:
            if m and getattr(m, "message_id", None):
                _add_msg_id(context, m.message_id)
        except Exception:
            pass
    except Exception:
        try:
            await update.message.reply_text(caption, reply_markup=kb)
        except Exception:
            await update.message.reply_text(caption)


async def cmd_deploy(update, context):
    try:
        set_last_chat_id(update.effective_chat.id)
    except Exception:
        pass
    args = context.args or []
    socials = None
    privacy = "public"
    dry_run_opt: Optional[bool] = None

    for a in args:
        if a.startswith("privacy="):
            privacy = a.split("=", 1)[1] or "public"
        elif a.startswith("socials="):
            val = a.split("=", 1)[1]
            socials = [x.strip() for x in val.split(",") if x.strip()]
        elif a.startswith("dry="):
            val = a.split("=", 1)[1].strip().lower()
            if val in ("1", "true", "yes", "on"):
                dry_run_opt = True
            elif val in ("0", "false", "no", "off"):
                dry_run_opt = False

    hist = load_video_history()
    if not hist:
        await update.message.reply_text("История пуста. Сначала выполните /generate")
        return

    last = hist[-1]
    video_path = last.get("video_path")
    thumbnail_path = last.get("thumbnail_path") or DEFAULT_THUMBNAIL
    source_url = last.get("source_url")
    audio_path = last.get("audio_path")

    if not video_path or not os.path.exists(video_path):
        await update.message.reply_text("Последний файл не найден на диске.")
        return

    await update.message.reply_text("Начинаю публикацию… Подождите, это может занять несколько минут.")

    loop = asyncio.get_running_loop()

    def run_deploy():
        def progress(msg: str):
            show = True
            if msg.startswith("⬆️ ") or (msg.startswith("✅ ") and "— завершено" in msg):
                show = False
            if show:
                asyncio.run_coroutine_threadsafe(update.message.reply_text(msg), loop)
        return deploy_to_socials(
            video_path=video_path,
            thumbnail_path=thumbnail_path,
            source_url=source_url or "",
            audio_path=audio_path,
            privacy=privacy,
            socials=socials,
            dry_run=_get_bot_dry_run(context) if dry_run_opt is None else dry_run_opt,
            progress=progress,
            source_chat_id=update.effective_chat.id,
        )

    links = await asyncio.to_thread(run_deploy)

    await update.message.reply_text("✅ Готово, видео успешно загружено")


def delete_generation_item_by_id(item_id: str) -> bool:
    items = load_video_history()
    idx = next((i for i, it in enumerate(items) if str(it.get("id")) == str(item_id)), None)
    if idx is None:
        return False
    item = items[idx]
    vp = item.get("video_path")
    tp = item.get("thumbnail_path")
    for p in [vp, tp]:
        try:
            if p and os.path.exists(p):
                os.remove(p)
        except Exception:
            pass
    try:
        items.pop(idx)
        save_video_history(items)
    except Exception:
        pass
    try:
        with open(HISTORY_FILE, "w", encoding="utf-8") as f:
            json.dump({"urls": []}, f, ensure_ascii=False, indent=2)
    except Exception:
        pass
    return True


async def on_callback_publish(update, context):
    q = update.callback_query
    await q.answer()
    data = q.data or ""
    item_id = data.split(":", 1)[1] if ":" in data else None
    if not item_id:
        await q.message.reply_text("Не удалось определить видео для публикации.")
        return

    hist = load_video_history()
    item = next((it for it in hist if str(it.get("id")) == str(item_id)), None)
    if not item:
        await q.message.reply_text("Элемент истории не найден.")
        return
    video_path = item.get("video_path")
    thumbnail_path = item.get("thumbnail_path") or DEFAULT_THUMBNAIL
    source_url = item.get("source_url") or ""
    audio_path = item.get("audio_path")
    if not video_path or not os.path.exists(video_path):
        await q.message.reply_text("Файл видео отсутствует на диске.")
        return

    await q.message.reply_text("Начинаю публикацию… Подождите, это займёт несколько минут.")

    loop = asyncio.get_running_loop()

    def run_deploy():
        def progress(msg: str):
            show = True
            if msg.startswith("⬆️ ") or (msg.startswith("✅ ") and "— завершено" in msg):
                show = False
            if show:
                asyncio.run_coroutine_threadsafe(q.message.reply_text(msg), loop)
        return deploy_to_socials(
            video_path=video_path,
            thumbnail_path=thumbnail_path,
            source_url=source_url,
            audio_path=audio_path,
            privacy="public",
            socials=None,
            dry_run=_get_bot_dry_run(context),
            progress=progress,
            source_chat_id=update.effective_chat.id,
        )

    links = await asyncio.to_thread(run_deploy)
    if isinstance(links, dict):
        for it in hist:
            if str(it.get("id")) == str(item_id):
                it["deployment_links"] = links
                break
        save_video_history(hist)
    await q.message.reply_text("✅ Готово, видео успешно загружено")


def _get_selected_socials_store(context):
    store = context.chat_data.get("selected_socials")
    if not isinstance(store, dict):
        store = {}
        context.chat_data["selected_socials"] = store
    return store


def _get_selection_for_item(context, item_id: str) -> set[str]:
    store = _get_selected_socials_store(context)
    sel = store.get(str(item_id))
    if not isinstance(sel, set):
        sel = set()
        store[str(item_id)] = sel
    return sel
def _platforms_keyboard(item_id: str, selection: set[str]):
    if not (InlineKeyboardButton and InlineKeyboardMarkup):
        return None
    def label(name):
        return ("✅ " + name.capitalize()) if name in selection else ("❌ " + name.capitalize())
    rows = [
        [
            InlineKeyboardButton(label("youtube"), callback_data=f"toggle:youtube:{item_id}"),
            InlineKeyboardButton(label("instagram"), callback_data=f"toggle:instagram:{item_id}"),
        ],
        [
            InlineKeyboardButton(label("x"), callback_data=f"toggle:x:{item_id}"),
        ],
        [
            InlineKeyboardButton("Опубликовать выбранные", callback_data=f"publishsel:{item_id}"),
        ],
        [
            InlineKeyboardButton("Опубликовать все", callback_data=f"publishall:{item_id}"),
            InlineKeyboardButton("Отмена", callback_data=f"cancelchoose:{item_id}"),
        ],
    ]
    return InlineKeyboardMarkup(rows)
async def on_callback_choose_platforms(update, context):
    q = update.callback_query
    await q.answer()
    data = q.data or ""
    item_id = data.split(":", 1)[1] if ":" in data else None
    if not item_id:
        await q.message.reply_text("Не удалось определить элемент.")
        return
    sel = _get_selection_for_item(context, item_id)
    text = "Выберите платформы. Если ничего не выбрано, будет опубликовано во все:"
    kb = _platforms_keyboard(item_id, sel)
    try:
        await q.message.reply_text(text, reply_markup=kb)
    except Exception:
        pass

async def on_callback_toggle_platform(update, context):
    q = update.callback_query
    await q.answer()
    data = q.data or ""
    try:
        _, platform, item_id = data.split(":", 2)
    except Exception:
        await q.message.reply_text("Некорректные данные кнопки.")
        return
    sel = _get_selection_for_item(context, item_id)
    platform = platform.lower()
    if platform in sel:
        sel.remove(platform)
    else:
        if platform in ALL_SOCIALS:
            sel.add(platform)
    kb = _platforms_keyboard(item_id, sel)
    try:
        await q.message.edit_reply_markup(reply_markup=kb)
    except Exception:
        try:
            await q.message.reply_text("Обновлено", reply_markup=kb)
        except Exception:
            pass


async def on_callback_publish_selected(update, context):
    q = update.callback_query
    await q.answer()
    data = q.data or ""
    item_id = data.split(":", 1)[1] if ":" in data else None
    if not item_id:
        await q.message.reply_text("Не удалось определить элемент для публикации.")
        return
    hist = load_video_history()
    item = next((it for it in hist if str(it.get("id")) == str(item_id)), None)
    if not item:
        await q.message.reply_text("Элемент истории не найден.")
        return
    video_path = item.get("video_path")
    thumbnail_path = item.get("thumbnail_path") or DEFAULT_THUMBNAIL
    source_url = item.get("source_url") or ""
    audio_path = item.get("audio_path")
    if not video_path or not os.path.exists(video_path):
        await q.message.reply_text("Файл видео отсутствует на диске.")
        return
    sel = _get_selection_for_item(context, item_id)
    socials = sorted(sel) if sel else None
    await q.message.reply_text("Начинаю публикацию выбранных платформ…")
    loop = asyncio.get_running_loop()
    def run_deploy():
        def progress(msg: str):
            show = True
            if msg.startswith("⬆️ ") or (msg.startswith("✅ ") and "— завершено" in msg):
                show = False
            if show:
                asyncio.run_coroutine_threadsafe(q.message.reply_text(msg), loop)
        return deploy_to_socials(
            video_path=video_path,
            thumbnail_path=thumbnail_path,
            source_url=source_url,
            audio_path=audio_path,
            privacy="public",
            socials=socials,
            dry_run=_get_bot_dry_run(context),
            progress=progress,
            source_chat_id=update.effective_chat.id,
        )
    links = await asyncio.to_thread(run_deploy)
    if isinstance(links, dict):
        for it in hist:
            if str(it.get("id")) == str(item_id):
                it["deployment_links"] = links
                break
        save_video_history(hist)
    await q.message.reply_text("✅ Готово, видео успешно загружено")


async def on_callback_publish_all(update, context):
    q = update.callback_query
    await q.answer()
    data = q.data or ""
    item_id = data.split(":", 1)[1] if ":" in data else None
    if not item_id:
        await q.message.reply_text("Не удалось определить элемент для публикации.")
        return
    hist = load_video_history()
    item = next((it for it in hist if str(it.get("id")) == str(item_id)), None)
    if not item:
        await q.message.reply_text("Элемент истории не найден.")
        return
    video_path = item.get("video_path")
    thumbnail_path = item.get("thumbnail_path") or DEFAULT_THUMBNAIL
    source_url = item.get("source_url") or ""
    audio_path = item.get("audio_path")
    if not video_path or not os.path.exists(video_path):
        await q.message.reply_text("Файл видео отсутствует на диске.")
        return
    await q.message.reply_text("Начинаю публикацию во все платформы…")
    loop = asyncio.get_running_loop()
    def run_deploy():
        def progress(msg: str):
            show = True
            if msg.startswith("⬆️ ") or (msg.startswith("✅ ") and "— завершено" in msg):
                show = False
            if show:
                asyncio.run_coroutine_threadsafe(q.message.reply_text(msg), loop)
        return deploy_to_socials(
            video_path=video_path,
            thumbnail_path=thumbnail_path,
            source_url=source_url,
            audio_path=audio_path,
            privacy="public",
            socials=None,
            dry_run=_get_bot_dry_run(context),
            progress=progress,
            source_chat_id=update.effective_chat.id,
        )
    links = await asyncio.to_thread(run_deploy)
    if isinstance(links, dict):
        for it in hist:
            if str(it.get("id")) == str(item_id):
                it["deployment_links"] = links
                break
        save_video_history(hist)
    await q.message.reply_text("✅ Готово, видео успешно загружено")


async def on_callback_cancel_choose(update, context):
    q = update.callback_query
    await q.answer()
    try:
        await q.message.delete()
    except Exception:
        pass


async def on_callback_regenerate(update, context):
    q = update.callback_query
    await q.answer()
    data = q.data or ""
    item_id = data.split(":", 1)[1] if ":" in data else None
    if not item_id:
        await q.message.reply_text("Не удалось определить элемент для перегенерации.")
        return

    deleted = delete_generation_item_by_id(item_id)
    if not deleted:
        await q.message.reply_text("Не удалось удалить предыдущую генерацию.")
        return

    try:
        await q.message.delete()
    except Exception:
        pass

    pmid = context.chat_data.pop("progress_msg_id", None)
    if pmid:
        try:
            await context.bot.delete_message(chat_id=update.effective_chat.id, message_id=pmid)
        except Exception:
            pass

    ids_to_delete = _pop_all_msg_ids(context)
    if ids_to_delete:
        for mid in set(ids_to_delete):
            try:
                await context.bot.delete_message(chat_id=update.effective_chat.id, message_id=mid)
            except Exception:
                pass

    context.chat_data.pop("progress_lines", None)
    await _progress_init(context, update.effective_chat.id, "Удалил предыдущее видео и логи. Начинаю новую генерацию…")

    args = context.args or []
    pin_num = parse_int(args[0], 50) if len(args) >= 1 else 50
    audio_duration = parse_int(args[1], 10) if len(args) >= 2 else 10
    pinterest_urls = load_urls_json(DEFAULT_PINTEREST_JSON, [])
    music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
    reddit_sources = load_urls_json(DEFAULT_REDDIT_JSON, [])
    twitter_sources = load_urls_json(DEFAULT_TWITTER_JSON, [])

    loop = asyncio.get_running_loop()

    def run_generation():
        def progress(msg: str):
            asyncio.run_coroutine_threadsafe(_progress_queue(context, update.effective_chat.id, msg), loop)
        return generate_meme_video(
            pinterest_urls=pinterest_urls,
            music_playlists=music_playlists,
            pin_num=pin_num,
            audio_duration=audio_duration,
            progress=progress,
            reddit_sources=reddit_sources,
            twitter_sources=twitter_sources,
        )

    result = await asyncio.to_thread(run_generation)
    if not result or not result.video_path:
        await q.message.reply_text("Не удалось создать новое видео.")
        return
    new_item = add_video_history_item(result.video_path, result.thumbnail_path, result.source_url, result.audio_path)
    caption = _format_video_info(result)
    kb = None
    if InlineKeyboardButton and InlineKeyboardMarkup:
        kb = InlineKeyboardMarkup(
            [
                [InlineKeyboardButton("Опубликовать", callback_data=f"publish:{new_item['id']}")],
                [InlineKeyboardButton("Выбрать платформы", callback_data=f"choose:{new_item['id']}")],
                [InlineKeyboardButton("Сменить трек", callback_data=f"changeaudio:{new_item['id']}")],
                [InlineKeyboardButton("Сгенерировать заново", callback_data=f"regenerate:{new_item['id']}")],
            ]
        )
    try:
        m = await context.bot.send_video(chat_id=update.effective_chat.id, video=open(result.video_path, "rb"), caption=caption, reply_markup=kb)
        try:
            if m and getattr(m, "message_id", None):
                _add_msg_id(context, m.message_id)
        except Exception:
            pass
    except Exception:
        try:
            await q.message.reply_text(caption, reply_markup=kb)
        except Exception:
            await q.message.reply_text(caption)


async def on_callback_change_audio(update, context):
    q = update.callback_query
    await q.answer()
    data = q.data or ""
    item_id = data.split(":", 1)[1] if ":" in data else None
    if not item_id:
        await q.message.reply_text("Не удалось определить видео для смены аудио.")
        return

    hist = load_video_history()
    item = next((it for it in hist if str(it.get("id")) == str(item_id)), None)
    if not item:
        await q.message.reply_text("Элемент истории не найден.")
        return
    
    video_path = item.get("video_path")
    if not video_path or not os.path.exists(video_path):
        await q.message.reply_text("Файл видео отсутствует на диске.")
        return

    music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
    if not music_playlists:
        await q.message.reply_text("Нет доступных плейлистов для замены аудио.")
        return

    await q.message.reply_text("🎵 Начинаю замену аудиотрека...")
    
    loop = asyncio.get_running_loop()
    
    def run_audio_replacement():
        def progress(msg: str):
            asyncio.run_coroutine_threadsafe(q.message.reply_text(msg), loop)
        
        return replace_audio_in_video(
            video_path=video_path,
            music_playlists=music_playlists,
            audio_duration=12,
            progress=progress
        )

    result = await asyncio.to_thread(run_audio_replacement)
    
    if not result or not result.video_path:
        await q.message.reply_text("❌ Не удалось заменить аудио в видео.")
        return
    
    # Удаляем старое видео из истории
    hist = [it for it in hist if str(it.get("id")) != str(item_id)]
    save_video_history(hist)
    
    # Удаляем старые файлы
    old_video = item.get("video_path")
    old_thumbnail = item.get("thumbnail_path")
    for p in [old_video, old_thumbnail]:
        try:
            if p and os.path.exists(p):
                os.remove(p)
        except Exception:
            pass
    
    # Добавляем новое видео в историю
    new_item = add_video_history_item(
        result.video_path, 
        result.thumbnail_path, 
        item.get("source_url"), 
        None,  # audio_path
        None   # deployment_links
    )
    
    caption = f"✅ Аудио заменено!\n"
    if result.audio_title:
        caption += f"🎵 Новый трек: {result.audio_title}\n"
    if item.get("source_url"):
        caption += f"📎 Источник: {item.get('source_url')}"
    
    kb = None
    if InlineKeyboardButton and InlineKeyboardMarkup:
        kb = InlineKeyboardMarkup([
            [InlineKeyboardButton("Опубликовать", callback_data=f"publish:{new_item['id']}")],
            [InlineKeyboardButton("Выбрать платформы", callback_data=f"choose:{new_item['id']}")],
            [InlineKeyboardButton("Сменить трек", callback_data=f"changeaudio:{new_item['id']}")],
            [InlineKeyboardButton("Сгенерировать заново", callback_data=f"regenerate:{new_item['id']}")],
        ])
    
    try:
        m = await context.bot.send_video(
            chat_id=update.effective_chat.id, 
            video=open(result.video_path, "rb"), 
            caption=caption, 
            reply_markup=kb
        )
        try:
            if m and getattr(m, "message_id", None):
                _add_msg_id(context, m.message_id)
        except Exception:
            pass
    except Exception:
        try:
            await q.message.reply_text(caption, reply_markup=kb)
        except Exception:
            await q.message.reply_text(caption)


async def cmd_history(update, context):
    hist = load_video_history()
    if not hist:
        await update.message.reply_text("История пуста")
        return
    lines = []
    for item in hist[-10:]:
        lines.append(
            f"#{item.get('id')} — {item.get('title')} — {item.get('video_path')}"
        )
    await update.message.reply_text("\n".join(lines))


async def cmd_uploadytcookies(update, context):
    doc = getattr(update.message, "document", None)
    if not doc:
        context.chat_data["await_upload"] = "ytcookies"
        await update.message.reply_text("Пришлите youtube_cookies.txt как документ следующим сообщением (ожидаю YouTube cookies)")
        return
    try:
        file = await context.bot.get_file(doc.file_id)
        data = await file.download_as_bytearray()
        ok = replace_file_from_bytes(YT_COOKIES_FILE, bytes(data))
        await update.message.reply_text("youtube_cookies.txt обновлён" if ok else "Не удалось сохранить youtube_cookies.txt")
    except Exception as e:
        await update.message.reply_text(f"Ошибка: {e}")




async def cmd_uploadclient(update, context):
    doc = getattr(update.message, "document", None)
    if not doc:
        context.chat_data["await_upload"] = "client"
        await update.message.reply_text("Пришлите client_secrets.json как документ следующим сообщением (ожидаю client)")
        return
    try:
        file = await context.bot.get_file(doc.file_id)
        data = await file.download_as_bytearray()
        ok = replace_file_from_bytes(CLIENT_SECRETS, bytes(data))
        await update.message.reply_text("client_secrets.json обновлён" if ok else "Не удалось сохранить client_secrets.json")
    except Exception as e:
        await update.message.reply_text(f"Ошибка: {e}")


async def cmd_uploadtoken(update, context):
    doc = getattr(update.message, "document", None)
    if not doc:
        context.chat_data["await_upload"] = "token"
        await update.message.reply_text("Пришлите token.pickle как документ следующим сообщением (ожидаю token)")
        return
    try:
        file = await context.bot.get_file(doc.file_id)
        data = await file.download_as_bytearray()
        ok = replace_file_from_bytes(TOKEN_PICKLE, bytes(data))
        await update.message.reply_text("token.pickle обновлён" if ok else "Не удалось сохранить token.pickle")
    except Exception as e:
        await update.message.reply_text(f"Ошибка: {e}")


async def cmd_clearhistory(update, context):
    ok = clear_video_history("video_history.json")
    await update.message.reply_text("История очищена" if ok else "Не удалось очистить историю")


async def cmd_checkfiles(update, context):
    try:
        set_last_chat_id(update.effective_chat.id)
    except Exception:
        pass
    paths = {
        "YouTube youtube_cookies.txt": YT_COOKIES_FILE,
        "YouTube client_secrets.json": CLIENT_SECRETS,
        "YouTube token.pickle": TOKEN_PICKLE,
    }
    lines = ["Проверка обязательных файлов:"]
    for label, p in paths.items():
        try:
            if os.path.isdir(p):
                lines.append(f"- {label}: ⚠️ указан путь — директория (ожидается файл) (путь: {p})")
            else:
                exists = os.path.isfile(p)
                size = os.path.getsize(p) if exists else 0
                status = "✅ найден" if exists and size > 0 else ("⚠️ пустой файл" if exists else "❌ отсутствует")
                lines.append(f"- {label}: {status} (путь: {p})")
        except Exception as e:
            lines.append(f"- {label}: ошибка проверки ({e})")
    lines.append("")
    if not os.path.exists(YT_COOKIES_FILE):
        lines.append("Для YouTube загрузите youtube_cookies.txt командой /uploadytcookies")
    missing_youtube = []
    if not os.path.exists(CLIENT_SECRETS):
        missing_youtube.append("client_secrets.json")
    if not os.path.exists(TOKEN_PICKLE):
        missing_youtube.append("token.pickle")
    if missing_youtube:
        lines.append(
            "Для YouTube загрузите: "
            + ", ".join(missing_youtube)
            + ". Команды: /uploadclient и /uploadtoken"
        )
    await update.message.reply_text("\n".join(lines))


async def cmd_pinterestcheck(update, context):
    """Check Pinterest status and availability"""
    try:
        set_last_chat_id(update.effective_chat.id)
    except Exception:
        pass
    
    try:
        from app.pinterest_monitor import get_pinterest_monitor
        
        await update.message.reply_text("Проверяю состояние Pinterest...")
        
        monitor = get_pinterest_monitor()
        
        # Force immediate check
        is_available = monitor.force_check()
        status = monitor.get_status_info()
        
        lines = ["🔍 Состояние Pinterest:"]
        
        if is_available:
            lines.append("✅ Pinterest доступен")
        else:
            lines.append("❌ Pinterest заблокирован или недоступен")
        
        if status['fallback_mode']:
            lines.append("⚠️ Активен режим резервного источника контента")
        else:
            lines.append("✅ Используется Pinterest как основной источник")
        
        lines.append(f"📊 Последовательных ошибок: {status['consecutive_failures']}")
        lines.append(f"⏰ Последняя проверка: {status['last_check_seconds_ago']} сек. назад")
        lines.append(f"✅ Последний успешный доступ: {status['last_success_seconds_ago']} сек. назад")
        
        if status['recovery_attempts'] > 0:
            lines.append(f"🔄 Попытки восстановления: {status['recovery_attempts']}")
        
        if status['fallback_mode']:
            lines.append("\n💡 В режиме резервного источника используются альтернативные meme API")
            lines.append("Pinterest будет автоматически проверяться каждые 5 минут")
        
        await update.message.reply_text("\n".join(lines))
        
    except Exception as e:
        await update.message.reply_text(f"Ошибка при проверке Pinterest: {e}")


async def on_document_received(update, context):
    doc = getattr(update.message, "document", None)
    if not doc:
        return
    purpose = context.chat_data.pop("await_upload", None)
    fname = (getattr(doc, "file_name", "") or "").lower()
    target = None
    if purpose == "ytcookies" or fname == "youtube_cookies.txt" or fname.endswith("/youtube_cookies.txt"):
        target = YT_COOKIES_FILE
    elif purpose == "client" or fname == "client_secrets.json" or fname.endswith("/client_secrets.json"):
        target = CLIENT_SECRETS
    elif purpose == "token" or fname == "token.pickle" or fname.endswith("/token.pickle"):
        target = TOKEN_PICKLE

    else:
        if fname.endswith("youtube_cookies.txt"):
            target = YT_COOKIES_FILE
        elif fname.endswith("client_secrets.json"):
            target = CLIENT_SECRETS
        elif fname.endswith("token.pickle"):
            target = TOKEN_PICKLE
    if not target:
        await update.message.reply_text("Неизвестный файл. Ожидаю cookies.txt, youtube_cookies.txt, client_secrets.json или token.pickle")
        return
    try:
        file = await context.bot.get_file(doc.file_id)
        bio = BytesIO()
        await file.download_to_memory(out=bio)
        ok = replace_file_from_bytes(target, bio.getvalue())
        await update.message.reply_text((f"Файл сохранён: {target}") if ok else "Не удалось сохранить файл")
    except Exception as e:
        await update.message.reply_text(f"Ошибка: {e}")


def _random_time_today_tomsk() -> datetime:
    tz = _tz_tomsk()
    now = datetime.now(tz)
    start = now.replace(hour=10, minute=0, second=0, microsecond=0)
    # end is exclusive midnight of next day (window 10:00 – 24:00)
    end = (start + timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0) + timedelta(days=0)
    if now >= end:
        tomorrow = now + timedelta(days=1)
        start = tomorrow.replace(hour=10, minute=0, second=0, microsecond=0)
        end = (start + timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0)
    total_seconds = int((end - start).total_seconds())
    if total_seconds <= 0:
        total_seconds = 12 * 3600
    offset = int(os.urandom(2).hex(), 16) % total_seconds
    return start + timedelta(seconds=offset)


def _random_time_tomsk_for_date(date_obj) -> datetime:
    tz = _tz_tomsk()
    start = datetime(year=date_obj.year, month=date_obj.month, day=date_obj.day, hour=10, minute=0, second=0, microsecond=0, tzinfo=tz)
    # exclusive midnight next day
    end = (start + timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0) + timedelta(days=0)
    total_seconds = int((end - start).total_seconds())
    if total_seconds <= 0:
        total_seconds = 12 * 3600
    offset = int(os.urandom(2).hex(), 16) % total_seconds
    return start + timedelta(seconds=offset)


def _compute_next_target(now: datetime) -> datetime:
    tz = _tz_tomsk()
    saved = get_next_run_iso()
    if saved:
        try:
            saved_dt = datetime.fromisoformat(saved)
            if saved_dt.tzinfo is None:
                saved_dt = saved_dt.replace(tzinfo=tz)
            if saved_dt > now:
                logging.info(f"Используется сохранённое время: {saved_dt}")
                return saved_dt
        except Exception:
            pass
    def _random_time_between(start_dt: datetime, end_dt: datetime) -> datetime:
        total_seconds = int((end_dt - start_dt).total_seconds())
        if total_seconds <= 0:
            return end_dt
        offset = int(os.urandom(3).hex(), 16) % total_seconds
        return start_dt + timedelta(seconds=offset)

    start_today = now.replace(hour=10, minute=0, second=0, microsecond=0)
    end_today = (now.replace(hour=10, minute=0, second=0, microsecond=0) + timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0)

    if now < start_today:
        cand = _random_time_between(start_today, end_today)
        logging.info(f"Вычислено время на сегодня (до окна): {cand}")
        return cand
    if now < end_today:
        start_range = now + timedelta(seconds=1)
        cand = _random_time_between(start_range, end_today)
        logging.info(f"Вычислено время на сегодня (внутри окна): {cand}")
        return cand

    next_day = now + timedelta(days=1)
    start_next = next_day.replace(hour=10, minute=0, second=0, microsecond=0)
    end_next = (next_day.replace(hour=10, minute=0, second=0, microsecond=0) + timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0)
    cand = _random_time_between(start_next, end_next)
    logging.info(f"Вычислено время на завтра (после окна): {cand}")
    return cand


def _reschedule_to(app, target_dt: datetime) -> None:
    tz = _tz_tomsk()
    if target_dt.tzinfo is None:
        target_dt = target_dt.replace(tzinfo=tz)
    try:
        old_job = app.bot_data.get("scheduled_job")
        if old_job is not None:
            try:
                old_job.schedule_removal()
            except Exception:
                pass
    except Exception:
        pass
    now = datetime.now(tz)
    delay = max(1, int((target_dt - now).total_seconds()))
    try:
        job = app.job_queue.run_once(_scheduled_job, when=delay)
        app.bot_data["scheduled_job"] = job
        app.bot_data["scheduled_target_iso"] = target_dt.isoformat()
        try:
            set_next_run_iso(target_dt.isoformat())
        except Exception:
            pass
        logging.info(f"Перепланировано на {target_dt} (через {delay} сек)")
    except Exception as e:
        logging.error(f"Не удалось перепланировать задачу: {e}")


async def _scheduled_job(context):
    logging.info("Запуск запланированной генерации видео (мульти режим)")
    app = context.application
    tz = _tz_tomsk()
    tznow = datetime.now(tz)
    cid = _resolve_notify_chat_id()
    
    if _is_generation_locked(app):
        lock_time = app.bot_data.get('generation_lock_time', 0)
        elapsed = int(time.time() - lock_time)
        logging.warning(f"Пропуск запланированной генерации - уже выполняется (запущена {elapsed} сек. назад)")
        if cid:
            try:
                await app.bot.send_message(chat_id=cid, text=f"⚠️ Пропуск запланированной генерации - другая генерация уже выполняется ({elapsed} сек.)")
            except Exception:
                pass
        return
    
    if not _lock_generation(app):
        logging.warning("Не удалось заблокировать генерацию для запланированной задачи")
        return
    
    try:
        save_generation_state("started", {"time": tznow.isoformat()})
        log_resource_usage("scheduled_start")
        
        schedule_list = get_daily_schedule_iso()
        if schedule_list:
            schedule_list = sorted(schedule_list)
            schedule_list = [x for x in schedule_list if datetime.fromisoformat(x) > tznow]
            set_daily_schedule_iso(schedule_list)
        pinterest_urls = load_urls_json(DEFAULT_PINTEREST_JSON, [])
        music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
        reddit_sources = load_urls_json(DEFAULT_REDDIT_JSON, [])
        twitter_sources = load_urls_json(DEFAULT_TWITTER_JSON, [])
        
        if not cid:
            logging.warning("Не найден chat_id для уведомления")
    except Exception as e:
        logging.error(f"Ошибка инициализации запланированной генерации: {e}", exc_info=True)
        save_generation_state("init_error", {"error": str(e), "time": datetime.now(tz).isoformat()})
        if cid:
            try:
                await app.bot.send_message(chat_id=cid, text=f"❌ Ошибка инициализации генерации:\n{e}")
            except Exception:
                pass
        _unlock_generation(app)
        return
    
    try:
        cleanup_old_temp_dirs()
    except Exception as e:
        logging.warning(f"Ошибка при очистке временных директорий: {e}")
    
    async def gen_one(idx: int, attempt: int):
        def run_generation():
            try:
                logging.info(f"Генерация #{idx}, попытка {attempt}")
                mem_before = log_resource_usage(f"gen_{idx}_{attempt}_start")
                save_generation_state("generating", {"index": idx, "attempt": attempt, "time": datetime.now(tz).isoformat(), "memory": mem_before})
                seed = int.from_bytes(os.urandom(4), 'big') ^ (idx + attempt * 9973)
                result = generate_meme_video(pinterest_urls, music_playlists, pin_num=10000, audio_duration=10, seed=seed, variant_group=idx % 5, reddit_sources=reddit_sources, twitter_sources=twitter_sources)
                mem_after = log_resource_usage(f"gen_{idx}_{attempt}_end")
                logging.info(f"Генерация #{idx}, попытка {attempt} завершена успешно")
                gc.collect()
                return result
            except Exception as e:
                mem_error = get_memory_info()
                logging.error(f"Критическая ошибка в генерации #{idx}, попытка {attempt}: {e}", exc_info=True)
                save_generation_state("generation_error", {"index": idx, "attempt": attempt, "error": str(e), "time": datetime.now(tz).isoformat(), "memory": mem_error})
                gc.collect()
                return None
        return await asyncio.to_thread(run_generation)
    
    gens = []
    try:
        for i in range(3):
            logging.info(f"Запуск генерации варианта {i+1}/3")
            result = await gen_one(i, 0)
            gens.append(result)
            logging.info(f"Генерация варианта {i+1}/3 завершена")
    except Exception as e:
        logging.error(f"Критическая ошибка при создании вариантов: {e}", exc_info=True)
        save_generation_state("batch_error", {"error": str(e), "time": datetime.now(tz).isoformat()})
        if cid:
            try:
                await app.bot.send_message(chat_id=cid, text=f"❌ Критическая ошибка при генерации:\n{e}")
            except Exception:
                pass
        return
    
    try:
        def result_source(res):
            return getattr(res,'source_url', None) if res else None
        seen = set()
        for attempt in range(1, DUP_REGEN_RETRIES+1):
            dup_indexes = []
            seen.clear()
            for i,res in enumerate(gens):
                if isinstance(res, Exception) or not res:
                    dup_indexes.append(i)
                    continue
                src = result_source(res)
                if not src or src in seen:
                    dup_indexes.append(i)
                else:
                    seen.add(src)
            if not dup_indexes:
                break
            logging.info(f"Повторная генерация дубликатов, попытка {attempt}: индексы {dup_indexes}")
            for i in dup_indexes:
                new_res = await gen_one(i, attempt)
                gens[i] = new_res
    except Exception as e:
        logging.error(f"Ошибка при проверке дубликатов: {e}", exc_info=True)
        save_generation_state("dedup_error", {"error": str(e), "time": datetime.now(tz).isoformat()})
    
    results = []
    try:
        for res in gens:
            try:
                if isinstance(res, Exception):
                    results.append(None)
                    continue
                if not res:
                    results.append(None)
                    continue
                vp = getattr(res, 'video_path', None)
                tp = getattr(res, 'thumbnail_path', None)
                sp = getattr(res, 'source_url', None)
                ap = getattr(res, 'audio_path', None)
                if vp and os.path.exists(vp):
                    item = add_video_history_item(vp, tp, sp, ap)
                    results.append(item)
                else:
                    results.append(None)
            except Exception as e:
                logging.error(f"Ошибка обработки результата генерации: {e}", exc_info=True)
                results.append(None)
    except Exception as e:
        logging.error(f"Критическая ошибка при обработке результатов: {e}", exc_info=True)
        save_generation_state("results_error", {"error": str(e), "time": datetime.now(tz).isoformat()})
        if cid:
            try:
                await app.bot.send_message(chat_id=cid, text=f"❌ Ошибка обработки результатов:\n{e}")
            except Exception:
                pass
        return
    
    
    try:
        save_generation_state("completed", {"time": datetime.now(tz).isoformat(), "results_count": len([r for r in results if r])})
    except Exception as e:
        logging.error(f"Ошибка сохранения состояния завершения: {e}")
    
    if cid and results:
        try:
            generation_id = os.urandom(4).hex()
            valid_results = [r for r in results if r]
            
            if valid_results:
                # Используем слайдер для отправки результатов
                try:
                    msg_ids = await _send_video_slider(context, cid, valid_results, generation_id)
                except Exception as e:
                    logging.error(f"Ошибка отправки слайдера при автогенерации: {e}")
                    # Fallback на старый метод
                    msg_ids = []
                    try:
                        m0 = await app.bot.send_message(chat_id=cid, text="Автогенерация завершена. Отправлено 3 варианта ниже:")
                        if m0 and getattr(m0, 'message_id', None):
                            msg_ids.append(m0.message_id)
                    except Exception:
                        pass
                    
                    for item in valid_results:
                        vid = item['id']
                        kb = None
                        if InlineKeyboardButton and InlineKeyboardMarkup:
                            kb = InlineKeyboardMarkup([
                                [InlineKeyboardButton(f"Опубликовать #{vid}", callback_data=f"publish:{vid}")],
                                [InlineKeyboardButton(f"Выбрать платформы #{vid}", callback_data=f"choose:{vid}")],
                                [InlineKeyboardButton(f"Сменить трек #{vid}", callback_data=f"changeaudio:{vid}")],
                            ])
                        try:
                            if item.get('video_path') and os.path.exists(item.get('video_path')):
                                info_text = f"Кандидат #{vid}\n{_format_video_info_from_history(item)}"
                                mv = await app.bot.send_video(chat_id=cid, video=open(item.get('video_path'), 'rb'), caption=info_text, reply_markup=kb)
                                if mv and getattr(mv, 'message_id', None):
                                    msg_ids.append(mv.message_id)
                        except Exception as e:
                            logging.error(f"Ошибка отправки видео #{vid}: {e}")
                
                # Сохраняем данные для возможной регенерации
                try:
                    store = app.bot_data.get('scheduled_generations')
                    if not isinstance(store, dict):
                        store = {}
                        app.bot_data['scheduled_generations'] = store
                    store[generation_id] = {
                        'item_ids': [it['id'] for it in valid_results],
                        'msg_ids': msg_ids,
                        'chat_id': cid
                    }
                except Exception as e:
                    logging.error(f"Ошибка сохранения данных генерации: {e}")
        except Exception as e:
            logging.error(f"Критическая ошибка при отправке сообщений: {e}", exc_info=True)
            save_generation_state("send_error", {"error": str(e), "time": datetime.now(tz).isoformat()})
    
    try:
        schedule_list = get_daily_schedule_iso()
        tznow2 = datetime.now(tz)
        future = [datetime.fromisoformat(x) for x in schedule_list if datetime.fromisoformat(x) > tznow2]
        if future:
            next_dt = min(future)
            _reschedule_to(app, next_dt)
            app.bot_data['scheduled_target_iso'] = next_dt.isoformat()
        else:
            tomorrow = (tznow2 + timedelta(days=1)).date()
            times = _build_daily_schedule_for_date(tomorrow, DAILY_GENERATIONS)
            set_daily_schedule_iso([t.isoformat() for t in times])
            next_dt = times[0]
            _reschedule_to(app, next_dt)
            app.bot_data['scheduled_target_iso'] = next_dt.isoformat()
        logging.info(f"Сформировано следующее расписание: {get_daily_schedule_iso()}")
    except Exception as e:
        logging.error(f"Ошибка планирования следующей генерации: {e}", exc_info=True)
        if cid:
            try:
                await app.bot.send_message(chat_id=cid, text=f"⚠️ Ошибка планирования следующей генерации:\n{e}")
            except Exception:
                pass
    finally:
        _unlock_generation(app)
        try:
            log_resource_usage("scheduled_end")
        except Exception:
            pass


def _build_daily_schedule_for_date(date_obj, count: int) -> list[datetime]:
    tz = _tz_tomsk()
    start = datetime(year=date_obj.year, month=date_obj.month, day=date_obj.day, hour=10, minute=0, second=0, microsecond=0, tzinfo=tz)
    # exclusive midnight: next day 00:00
    end = (start + timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0)
    total_seconds = int((end - start).total_seconds())
    if count <= 1:
        return [start + timedelta(seconds=int(total_seconds / 2))]
    segment = total_seconds / count
    times = []
    # Jitter is limited to a fraction of the segment to keep slots spread out
    # This guarantees that adjacent items won't collapse too close (e.g., < 1 hour)
    jitter_max = int(max(0, min(1800, segment / 3)))
    for i in range(count):
        seg_start = start + timedelta(seconds=int(i * segment))
        seg_end = start + timedelta(seconds=int((i + 1) * segment))
        center = seg_start + timedelta(seconds=int(segment / 2))
        r = int.from_bytes(os.urandom(2), 'big')
        delta = (r % (2 * jitter_max + 1)) - jitter_max if jitter_max > 0 else 0
        t = center + timedelta(seconds=int(delta))
        if t < seg_start:
            t = seg_start
        if t >= seg_end:
            t = seg_end - timedelta(seconds=1)
        times.append(t)
    times.sort()
    return times

def _schedule_next_job(app):
    logging.info("Планирование списка ежедневных генераций")
    tz = _tz_tomsk()
    now = datetime.now(tz)
    today_list = get_daily_schedule_iso()
    parsed = []
    for iso in today_list:
        try:
            dt = datetime.fromisoformat(iso)
            if dt.tzinfo is None:
                dt = dt.replace(tzinfo=tz)
            parsed.append(dt)
        except Exception:
            pass
    parsed = [p for p in parsed if p.date() == now.date()]
    if not parsed:
        times = _build_daily_schedule_for_date(now.date(), DAILY_GENERATIONS)
        set_daily_schedule_iso([t.isoformat() for t in times])
        parsed = times
    future = [p for p in parsed if p > now]
    if not future:
        tomorrow = (now + timedelta(days=1)).date()
        times = _build_daily_schedule_for_date(tomorrow, DAILY_GENERATIONS)
        set_daily_schedule_iso([t.isoformat() for t in times])
        future = times
    next_dt = min(future)
    _reschedule_to(app, next_dt)
    app.bot_data['scheduled_target_iso'] = next_dt.isoformat()
    logging.info(f"Планирование завершено. Следующая: {next_dt}. Все: {get_daily_schedule_iso()}")


def main():
    try:
        from telegram.ext import ApplicationBuilder, CommandHandler, CallbackQueryHandler, MessageHandler, filters
    except Exception as e:
        print(f"python-telegram-bot не установлен: {e}")
        return

    token = TELEGRAM_BOT_TOKEN or os.getenv("TELEGRAM_BOT_TOKEN")
    if not token:
        print("Не задан TELEGRAM_BOT_TOKEN в .env")
        return

    async def on_startup(appinst):
        logging.info("Бот запускается, инициализация планировщика")
        try:
            cid = _resolve_notify_chat_id()
            if cid:
                try:
                    crash_info = load_last_crash_info()
                    startup_msg = "🤖 Бот перезапущен. Планировщик активен."
                    
                    if crash_info:
                        state = crash_info.get('state', 'unknown')
                        error = crash_info.get('error', 'Неизвестная ошибка')
                        crash_time = crash_info.get('time', 'неизвестно')
                        details = crash_info.get('details', {})
                        
                        state_names = {
                            'generation_error': 'Ошибка генерации видео',
                            'batch_error': 'Ошибка пакетной генерации',
                            'init_error': 'Ошибка инициализации',
                            'dedup_error': 'Ошибка проверки дубликатов',
                            'results_error': 'Ошибка обработки результатов',
                            'send_error': 'Ошибка отправки сообщений'
                        }
                        
                        startup_msg += f"\n\n⚠️ Обнаружена информация о предыдущей проблеме:\n"
                        startup_msg += f"📍 Этап: {state_names.get(state, state)}\n"
                        startup_msg += f"⏰ Время: {crash_time}\n"
                        startup_msg += f"❌ Ошибка: {error[:200]}\n"
                        
                        if 'index' in details:
                            startup_msg += f"🔢 Вариант: {details['index'] + 1}\n"
                        if 'attempt' in details:
                            startup_msg += f"🔄 Попытка: {details['attempt']}\n"
                        
                        clear_crash_info()
                    
                    try:
                        last_exc = get_last_uncaught_exception()
                        if last_exc and last_exc.get('summary'):
                            startup_msg += f"\n\n🧯 Последнее непойманное исключение:\n{last_exc['summary'][:300]}"
                    except Exception:
                        pass
                    try:
                        phases = get_recent_phases(15)
                        if phases:
                            startup_msg += "\n\n📜 Последние фазы:" + "\n" + "\n".join(phases)
                    except Exception:
                        pass

                    await appinst.bot.send_message(chat_id=cid, text=startup_msg)
                except Exception as e:
                    logging.warning(f"Не удалось отправить стартовое сообщение: {e}")
            else:
                logging.warning("Стартовое уведомление не отправлено: не найден chat_id (ни state, ни env)")
        except Exception:
            pass
        try:
            _schedule_next_job(appinst)
        except Exception as e:
            logging.error(f"Ошибка при инициализации планировщика: {e}")
        try:
            temp_removed = cleanup_old_temp_dirs()
            logging.info(f"Startup cleanup: removed {temp_removed} temporary directories")
        except Exception as e:
            logging.error(f"Error cleaning temp dirs on startup: {e}")
        try:
            stats = cleanup_old_generated_files(max_age_days=7, dry_run=False)
            logging.info(f"Startup cleanup: removed {stats['videos_removed']} videos ({stats['videos_size'] / (1024*1024):.1f} MB) and {stats['thumbnails_removed']} thumbnails ({stats['thumbnails_size'] / (1024*1024):.1f} MB)")
        except Exception as e:
            logging.error(f"Error cleaning old generated files on startup: {e}")
        try:
            async def watchdog():
                tz = _tz_tomsk()
                while True:
                    await asyncio.sleep(10)
                    try:
                        saved = get_next_run_iso()
                        bot_saved = appinst.bot_data.get("scheduled_target_iso")
                        if saved and saved != bot_saved:
                            try:
                                dt = datetime.fromisoformat(saved)
                                if dt.tzinfo is None:
                                    dt = dt.replace(tzinfo=tz)
                                _reschedule_to(appinst, dt)
                                logging.info("Watchdog: обнаружено изменение bot_state.json, перепланировано")
                                continue
                            except Exception:
                                pass
                        if saved:
                            try:
                                dt = datetime.fromisoformat(saved)
                                if dt.tzinfo is None:
                                    dt = dt.replace(tzinfo=tz)
                                if dt <= datetime.now(tz):
                                    new_dt = _compute_next_target(datetime.now(tz))
                                    _reschedule_to(appinst, new_dt)
                                    logging.info("Watchdog: найдено прошедшее время запуска, перепланировано на ближайшее допустимое")
                            except Exception:
                                pass
                    except Exception:
                        pass
            appinst.create_task(watchdog())
        except Exception as e:
            logging.error(f"Ошибка запуска watchdog: {e}")

    app = ApplicationBuilder().token(token).post_init(on_startup).build()
    try:
        app.bot_data["dry_run"] = BOT_DRY_RUN_DEFAULT
    except Exception:
        pass

    app.add_handler(CommandHandler("start", cmd_start))
    app.add_handler(CommandHandler("help", cmd_help))
    app.add_handler(CommandHandler("status", cmd_status))
    app.add_handler(CommandHandler("generate", cmd_generate))
    app.add_handler(CommandHandler("deploy", cmd_deploy))
    app.add_handler(CommandHandler("history", cmd_history))
    app.add_handler(CommandHandler("uploadytcookies", cmd_uploadytcookies))
    app.add_handler(CommandHandler("uploadclient", cmd_uploadclient))
    app.add_handler(CommandHandler("uploadtoken", cmd_uploadtoken))
    app.add_handler(CommandHandler("clearhistory", cmd_clearhistory))
    app.add_handler(CommandHandler("checkfiles", cmd_checkfiles))
    app.add_handler(CommandHandler("pinterestcheck", cmd_pinterestcheck))
    async def cmd_cleanup(update, context):
        try:
            set_last_chat_id(update.effective_chat.id)
        except Exception:
            pass
        try:
            temp_removed = cleanup_old_temp_dirs()
            
            args = context.args or []
            dry_run = 'dry' in args or 'dryrun' in args
            days = 7
            for arg in args:
                if arg.startswith('days='):
                    try:
                        days = int(arg.split('=')[1])
                    except:
                        pass
            
            stats = cleanup_old_generated_files(max_age_days=days, dry_run=dry_run)
            
            lines = ["🧹 Очистка завершена\n"]
            lines.append(f"📁 Временные директории: {temp_removed}")
            lines.append(f"\n🎬 Видео (старше {days} дней):")
            lines.append(f"  Удалено: {stats['videos_removed']} ({stats['videos_size'] / (1024*1024):.1f} МБ)")
            lines.append(f"  Оставлено: {stats['videos_kept']}")
            lines.append(f"\n🖼 Миниатюры (старше {days} дней):")
            lines.append(f"  Удалено: {stats['thumbnails_removed']} ({stats['thumbnails_size'] / (1024*1024):.1f} МБ)")
            lines.append(f"  Оставлено: {stats['thumbnails_kept']}")
            
            if dry_run:
                lines.append("\n⚠️ Режим DRY RUN - файлы не удалены")
                lines.append("Используйте /cleanup без 'dry' для реального удаления")
            
            await update.message.reply_text("\n".join(lines))
        except Exception as e:
            logging.error(f"Ошибка при выполнении команды /cleanup: {e}", exc_info=True)
            await update.message.reply_text(f"Ошибка очистки: {e}")
    app.add_handler(CommandHandler("cleanup", cmd_cleanup))
    app.add_handler(CallbackQueryHandler(on_callback_publish, pattern=r"^publish:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_choose_platforms, pattern=r"^choose:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_toggle_platform, pattern=r"^toggle:[A-Za-z]+:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_publish_selected, pattern=r"^publishsel:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_publish_all, pattern=r"^publishall:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_cancel_choose, pattern=r"^cancelchoose:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_regenerate, pattern=r"^regenerate:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_change_audio, pattern=r"^changeaudio:\d+"))
    
    # Handlers для слайдера видео
    async def on_callback_slider_prev(update, context):
        """Обработчик кнопки 'Назад' в слайдере."""
        q = update.callback_query
        await q.answer()
        data = q.data or ""
        generation_id = data.split(":", 1)[1] if ":" in data else None
        if not generation_id:
            return
        
        slider_data = context.application.bot_data.get('video_sliders', {}).get(generation_id)
        if not slider_data:
            await q.message.reply_text("Данные слайдера не найдены.")
            return
        
        current_idx = slider_data.get('current_idx', 0)
        new_idx = current_idx - 1
        
        if new_idx < 0:
            await q.answer("Это первое видео")
            return
        
        success = await _update_slider_video(context, generation_id, new_idx)
        if not success:
            await q.message.reply_text("Ошибка при переключении видео.")
    
    async def on_callback_slider_next(update, context):
        """Обработчик кнопки 'Вперёд' в слайдере."""
        q = update.callback_query
        await q.answer()
        data = q.data or ""
        generation_id = data.split(":", 1)[1] if ":" in data else None
        if not generation_id:
            return
        
        slider_data = context.application.bot_data.get('video_sliders', {}).get(generation_id)
        if not slider_data:
            await q.message.reply_text("Данные слайдера не найдены.")
            return
        
        current_idx = slider_data.get('current_idx', 0)
        total = len(slider_data.get('results', []))
        new_idx = current_idx + 1
        
        if new_idx >= total:
            await q.answer("Это последнее видео")
            return
        
        success = await _update_slider_video(context, generation_id, new_idx)
        if not success:
            await q.message.reply_text("Ошибка при переключении видео.")
    
    async def on_callback_slider_noop(update, context):
        """Обработчик для неактивных кнопок слайдера."""
        q = update.callback_query
        await q.answer()
    
    async def on_callback_slider_regen(update, context):
        """Обработчик кнопки 'Сгенерировать заново' в слайдере."""
        q = update.callback_query
        await q.answer()
        data = q.data or ""
        generation_id = data.split(":", 1)[1] if ":" in data else None
        if not generation_id:
            await q.message.reply_text("Не удалось определить генерацию.")
            return
        
        slider_data = context.application.bot_data.get('video_sliders', {}).get(generation_id)
        if not slider_data:
            await q.message.reply_text("Данные слайдера не найдены или уже удалены.")
            return
        
        item_ids = slider_data.get('results', [])
        msg_ids = slider_data.get('msg_ids', [])
        chat_id = slider_data.get('chat_id') or q.message.chat_id
        
        # Удаляем старые видео из истории
        try:
            hist = load_video_history()
            target_set = {str(i) for i in item_ids}
            remaining = []
            for it in hist:
                iid = str(it.get('id'))
                if iid in target_set:
                    for p in [it.get('video_path'), it.get('thumbnail_path')]:
                        try:
                            if p and os.path.exists(p):
                                os.remove(p)
                        except Exception:
                            pass
                else:
                    remaining.append(it)
            save_video_history(remaining)
        except Exception as e:
            logging.error(f"Ошибка удаления старых видео: {e}")
        
        # Удаляем сообщения слайдера
        for mid in set(msg_ids):
            try:
                await context.bot.delete_message(chat_id=chat_id, message_id=mid)
            except Exception:
                pass
        
        # Удаляем текущее видео
        try:
            await q.message.delete()
        except Exception:
            pass
        
        # Удаляем данные слайдера
        try:
            context.application.bot_data.get('video_sliders', {}).pop(generation_id, None)
        except Exception:
            pass
        
        await context.bot.send_message(chat_id=chat_id, text="Удалил предыдущие варианты. Начинаю новую генерацию…")
        
        # Запускаем новую генерацию
        pinterest_urls = load_urls_json(DEFAULT_PINTEREST_JSON, [])
        music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
        reddit_sources = load_urls_json(DEFAULT_REDDIT_JSON, [])
        twitter_sources = load_urls_json(DEFAULT_TWITTER_JSON, [])
        
        async def gen_one(idx: int, attempt: int):
            def run_generation():
                seed = int.from_bytes(os.urandom(4), 'big') ^ (idx + attempt * 8647)
                return generate_meme_video(
                    pinterest_urls, music_playlists, 
                    pin_num=10000, audio_duration=10, 
                    seed=seed, variant_group=idx % 5, 
                    reddit_sources=reddit_sources, 
                    twitter_sources=twitter_sources
                )
            return await asyncio.to_thread(run_generation)
        
        gens = []
        count = len(item_ids)  # Генерируем столько же, сколько было
        for i in range(count):
            result = await gen_one(i, 0)
            gens.append(result)
        
        # Проверка на дубликаты
        def result_source(res):
            return getattr(res, 'source_url', None) if res else None
        
        for attempt in range(1, DUP_REGEN_RETRIES + 1):
            seen = set()
            dup_idx = []
            for i, res in enumerate(gens):
                if isinstance(res, Exception) or not res:
                    dup_idx.append(i)
                    continue
                src = result_source(res)
                if not src or src in seen:
                    dup_idx.append(i)
                else:
                    seen.add(src)
            if not dup_idx:
                break
            for i in dup_idx:
                new_res = await gen_one(i, attempt)
                gens[i] = new_res
        
        # Сохраняем результаты
        new_results = []
        for res in gens:
            try:
                if isinstance(res, Exception) or not res:
                    continue
                vp = getattr(res, 'video_path', None)
                tp = getattr(res, 'thumbnail_path', None)
                sp = getattr(res, 'source_url', None)
                ap = getattr(res, 'audio_path', None)
                if vp and os.path.exists(vp):
                    it = add_video_history_item(vp, tp, sp, ap)
                    new_results.append(it)
            except Exception:
                pass
        
        if not new_results:
            await context.bot.send_message(chat_id=chat_id, text="Не удалось создать новые варианты")
            return
        
        # Отправляем новый слайдер
        new_gen_id = os.urandom(4).hex()
        try:
            await _send_video_slider(context, chat_id, new_results, new_gen_id)
        except Exception as e:
            logging.error(f"Ошибка отправки нового слайдера: {e}")
            await context.bot.send_message(chat_id=chat_id, text=f"Ошибка отправки результатов: {e}")
    
    app.add_handler(CallbackQueryHandler(on_callback_slider_prev, pattern=r"^slider_prev:[A-Fa-f0-9]+"))
    app.add_handler(CallbackQueryHandler(on_callback_slider_next, pattern=r"^slider_next:[A-Fa-f0-9]+"))
    app.add_handler(CallbackQueryHandler(on_callback_slider_noop, pattern=r"^slider_noop"))
    app.add_handler(CallbackQueryHandler(on_callback_slider_regen, pattern=r"^slider_regen:[A-Fa-f0-9]+"))
    
    async def on_callback_scheduled_regenerate(update, context):
        q = update.callback_query
        await q.answer()
        data = q.data or ""
        gen_id = data.split(":",1)[1] if ":" in data else None
        if not gen_id:
            await q.message.reply_text("Не удалось определить генерацию.")
            return
        store = context.application.bot_data.get('scheduled_generations')
        entry = store.get(gen_id) if isinstance(store, dict) else None
        if not entry:
            await q.message.reply_text("Данные генерации не найдены или уже удалены.")
            return
        item_ids = entry.get('item_ids') or []
        msg_ids = entry.get('msg_ids') or []
        chat_id = entry.get('chat_id') or q.message.chat_id
        try:
            hist = load_video_history()
            target_set = {str(i) for i in item_ids}
            remaining = []
            for it in hist:
                iid = str(it.get('id'))
                if iid in target_set:
                    for p in [it.get('video_path'), it.get('thumbnail_path')]:
                        try:
                            if p and os.path.exists(p):
                                os.remove(p)
                        except Exception:
                            pass
                else:
                    remaining.append(it)
            save_video_history(remaining)
        except Exception:
            pass
        for mid in set(msg_ids):
            try:
                await context.bot.delete_message(chat_id=chat_id, message_id=mid)
            except Exception:
                pass
        try:
            if isinstance(store, dict):
                store.pop(gen_id, None)
        except Exception:
            pass
        await context.bot.send_message(chat_id=chat_id, text="Удалил предыдущие варианты. Начинаю новую генерацию…")
        pinterest_urls = load_urls_json(DEFAULT_PINTEREST_JSON, [])
        music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
        reddit_sources = load_urls_json(DEFAULT_REDDIT_JSON, [])
        twitter_sources = load_urls_json(DEFAULT_TWITTER_JSON, [])
        async def gen_one(idx:int, attempt:int):
            def run_generation():
                seed = int.from_bytes(os.urandom(4), 'big') ^ (idx + attempt * 8647)
                return generate_meme_video(pinterest_urls, music_playlists, pin_num=10000, audio_duration=10, seed=seed, variant_group=idx % 5, reddit_sources=reddit_sources, twitter_sources=twitter_sources)
            return await asyncio.to_thread(run_generation)
        
        gens = []
        for i in range(3):
            result = await gen_one(i, 0)
            gens.append(result)
        def result_source(res):
            return getattr(res,'source_url', None) if res else None
        for attempt in range(1, DUP_REGEN_RETRIES+1):
            seen = set()
            dup_idx = []
            for i,res in enumerate(gens):
                if isinstance(res, Exception) or not res:
                    dup_idx.append(i)
                    continue
                src = result_source(res)
                if not src or src in seen:
                    dup_idx.append(i)
                else:
                    seen.add(src)
            if not dup_idx:
                break
            for i in dup_idx:
                new_res = await gen_one(i, attempt)
                gens[i] = new_res
        new_results = []
        for res in gens:
            try:
                if isinstance(res, Exception) or not res:
                    continue
                vp = getattr(res, 'video_path', None)
                tp = getattr(res, 'thumbnail_path', None)
                sp = getattr(res, 'source_url', None)
                ap = getattr(res, 'audio_path', None)
                if vp and os.path.exists(vp):
                    it = add_video_history_item(vp, tp, sp, ap)
                    new_results.append(it)
            except Exception:
                pass
        if not new_results:
            await context.bot.send_message(chat_id=chat_id, text="Не удалось создать новые варианты")
            return
        
        new_gen_id = os.urandom(4).hex()
        
        # Используем слайдер для отправки новых результатов
        try:
            msg_ids2 = await _send_video_slider(context, chat_id, new_results, new_gen_id)
        except Exception as e:
            logging.error(f"Ошибка отправки слайдера при повторной генерации: {e}")
            # Fallback на старый метод
            msg_ids2 = []
            try:
                mhead = await context.bot.send_message(chat_id=chat_id, text="Готово. Новые варианты ниже:")
                if mhead and getattr(mhead,'message_id',None):
                    msg_ids2.append(mhead.message_id)
            except Exception:
                pass
            for it in new_results:
                vid = it['id']
                kb = None
                if InlineKeyboardButton and InlineKeyboardMarkup:
                    kb = InlineKeyboardMarkup([
                        [InlineKeyboardButton(f"Опубликовать #{vid}", callback_data=f"publish:{vid}")],
                        [InlineKeyboardButton(f"Выбрать платформы #{vid}", callback_data=f"choose:{vid}")],
                        [InlineKeyboardButton(f"Сменить трек #{vid}", callback_data=f"changeaudio:{vid}")],
                    ])
                try:
                    if it.get('video_path') and os.path.exists(it.get('video_path')):
                        info_text = f"Кандидат #{vid}\n{_format_video_info_from_history(it)}"
                        mv = await context.bot.send_video(chat_id=chat_id, video=open(it.get('video_path'),'rb'), caption=info_text, reply_markup=kb)
                    else:
                        info_text = f"Кандидат #{vid}\n{_format_video_info_from_history(it)}"
                        mv = await context.bot.send_message(chat_id=chat_id, text=info_text, reply_markup=kb)
                    if mv and getattr(mv,'message_id',None):
                        msg_ids2.append(mv.message_id)
                except Exception:
                    pass
        
        try:
            store2 = context.application.bot_data.get('scheduled_generations')
            if not isinstance(store2, dict):
                store2 = {}
                context.application.bot_data['scheduled_generations'] = store2
            store2[new_gen_id] = {'item_ids':[it['id'] for it in new_results], 'msg_ids':msg_ids2, 'chat_id':chat_id}
        except Exception:
            pass
    app.add_handler(CallbackQueryHandler(on_callback_scheduled_regenerate, pattern=r"^schedregen:[A-Fa-f0-9]+"))

    async def cmd_dryrun(update, context):
        args = context.args or []
        if not args:
            val = _get_bot_dry_run(context)
            await update.message.reply_text(f"Dry run: {'on' if val else 'off'}")
            return
        mode = args[0].strip().lower()
        if mode in ("on", "true", "1"):
            context.application.bot_data["dry_run"] = True
            await update.message.reply_text("Dry run включен: публикации выполняться не будут.")
        elif mode in ("off", "false", "0"):
            context.application.bot_data["dry_run"] = False
            await update.message.reply_text("Dry run выключен: публикации будут выполняться.")
        else:
            await update.message.reply_text("Используйте: /dryrun on|off")

    app.add_handler(CommandHandler("dryrun", cmd_dryrun))
    
    # Обработчики для загрузки видео и аудио
    async def on_video_received(update, context):
        """Обработчик загруженного видео от пользователя"""
        try:
            set_last_chat_id(update.effective_chat.id)
        except Exception:
            pass
        
        video = update.message.video
        if not video:
            return
        
        try:
            # Скачиваем видео
            file = await context.bot.get_file(video.file_id)
            
            import uuid
            import tempfile
            video_filename = f"user_video_{uuid.uuid4().hex[:8]}.mp4"
            video_path = os.path.join(tempfile.gettempdir(), video_filename)
            
            await update.message.reply_text("📥 Загружаю видео...")
            await file.download_to_drive(video_path)
            
            # Сохраняем путь к видео в контексте
            context.chat_data["uploaded_video_path"] = video_path
            
            # Предлагаем выбор аудио
            if InlineKeyboardButton and InlineKeyboardMarkup:
                kb = InlineKeyboardMarkup([
                    [InlineKeyboardButton("🎲 Случайный трек", callback_data="audio:random")],
                    [InlineKeyboardButton("📤 Загрузить свой аудио", callback_data="audio:upload")],
                    [InlineKeyboardButton("🔍 Поиск по плейлистам", callback_data="audio:search")],
                ])
                await update.message.reply_text(
                    "✅ Видео загружено!\n\n"
                    "Выберите способ добавления аудио:",
                    reply_markup=kb
                )
            else:
                await update.message.reply_text(
                    "✅ Видео загружено!\n\n"
                    "Отправьте команду:\n"
                    "/processrandom - случайный трек\n"
                    "/processsearch <запрос> - поиск трека"
                )
        
        except Exception as e:
            logging.error(f"Ошибка при обработке видео: {e}", exc_info=True)
            await update.message.reply_text(f"❌ Ошибка при загрузке видео: {e}")
    
    async def on_audio_received(update, context):
        """Обработчик загруженного аудио файла от пользователя"""
        try:
            set_last_chat_id(update.effective_chat.id)
        except Exception:
            pass
        
        audio = update.message.audio or update.message.voice or update.message.document
        if not audio:
            return
        
        # Проверяем, ждем ли мы аудио для видео
        video_path = context.chat_data.get("uploaded_video_path")
        if not video_path:
            await update.message.reply_text("⚠️ Сначала загрузите видео")
            return
        
        try:
            # Скачиваем аудио
            file = await context.bot.get_file(audio.file_id)
            
            import uuid
            import tempfile
            audio_ext = "mp3"
            if hasattr(audio, 'mime_type'):
                if 'wav' in audio.mime_type:
                    audio_ext = "wav"
                elif 'ogg' in audio.mime_type:
                    audio_ext = "ogg"
            
            audio_filename = f"user_audio_{uuid.uuid4().hex[:8]}.{audio_ext}"
            audio_path = os.path.join(tempfile.gettempdir(), audio_filename)
            
            await update.message.reply_text("📥 Загружаю аудио...")
            await file.download_to_drive(audio_path)
            
            # Извлекаем аудио в правильный формат
            from app.audio import extract_audio_from_file
            
            try:
                processed_audio = extract_audio_from_file(audio_path, output_dir=tempfile.gettempdir())
            except Exception as e:
                await update.message.reply_text(f"❌ Ошибка обработки аудио: {e}")
                return
            
            # Обрабатываем видео с этим аудио
            await process_video_with_selected_audio(update, context, video_path, processed_audio)
            
            # Очищаем временные файлы
            try:
                if os.path.exists(audio_path):
                    os.remove(audio_path)
            except Exception:
                pass
        
        except Exception as e:
            logging.error(f"Ошибка при обработке аудио: {e}", exc_info=True)
            await update.message.reply_text(f"❌ Ошибка при загрузке аудио: {e}")
    
    async def on_callback_audio_choice(update, context):
        """Обработчик выбора способа добавления аудио"""
        q = update.callback_query
        await q.answer()
        
        data = q.data or ""
        choice = data.split(":", 1)[1] if ":" in data else None
        
        if not choice:
            return
        
        video_path = context.chat_data.get("uploaded_video_path")
        if not video_path:
            await q.message.reply_text("⚠️ Видео не найдено. Загрузите видео заново.")
            return
        
        if choice == "random":
            # Случайный трек из плейлистов
            music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
            if not music_playlists:
                await q.message.reply_text("❌ Нет доступных плейлистов")
                return
            
            await process_video_with_selected_audio(update, context, video_path, None, music_playlists)
        
        elif choice == "upload":
            # Ожидаем загрузку аудио файла
            await q.message.reply_text(
                "📤 Загрузите аудио файл (MP3, WAV) или голосовое сообщение\n"
                "Я извлеку случайный фрагмент длиной 12 секунд"
            )
            context.chat_data["awaiting_audio_upload"] = True
        
        elif choice == "search":
            # Ожидаем поисковый запрос
            await q.message.reply_text(
                "🔍 Введите запрос для поиска трека\n"
                "Например: 'phonk' или 'lofi'\n\n"
                "Я найду подходящие треки в плейлистах"
            )
            context.chat_data["awaiting_search_query"] = True
    
    async def on_text_message(update, context):
        """Обработчик текстовых сообщений (для поиска треков)"""
        # Проверяем, ждем ли мы поисковый запрос
        if not context.chat_data.get("awaiting_search_query"):
            return
        
        video_path = context.chat_data.get("uploaded_video_path")
        if not video_path:
            await update.message.reply_text("⚠️ Видео не найдено. Загрузите видео заново.")
            context.chat_data["awaiting_search_query"] = False
            return
        
        search_query = update.message.text.strip()
        if not search_query:
            await update.message.reply_text("⚠️ Введите непустой запрос")
            return
        
        context.chat_data["awaiting_search_query"] = False
        
        # Ищем треки
        music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
        if not music_playlists:
            await update.message.reply_text("❌ Нет доступных плейлистов")
            return
        
        await update.message.reply_text(f"🔍 Ищу '{search_query}' в плейлистах...")
        
        loop = asyncio.get_running_loop()
        
        def search_tracks():
            from app.audio import search_tracks_in_playlists
            return search_tracks_in_playlists(music_playlists, search_query, max_results=10)
        
        try:
            results = await asyncio.to_thread(search_tracks)
            
            if not results:
                await update.message.reply_text(f"❌ Треки по запросу '{search_query}' не найдены")
                return
            
            # Сохраняем результаты и показываем кнопки
            context.chat_data["search_results"] = results
            
            if InlineKeyboardButton and InlineKeyboardMarkup:
                buttons = []
                for idx, (video_id, title, uploader) in enumerate(results[:10]):
                    display_text = f"{uploader} - {title}" if uploader else title
                    if len(display_text) > 60:
                        display_text = display_text[:57] + "..."
                    buttons.append([InlineKeyboardButton(display_text, callback_data=f"selecttrack:{idx}")])
                
                kb = InlineKeyboardMarkup(buttons)
                await update.message.reply_text(
                    f"✅ Найдено треков: {len(results)}\n\n"
                    "Выберите трек:",
                    reply_markup=kb
                )
            else:
                # Без кнопок - показываем список
                lines = [f"✅ Найдено треков: {len(results)}\n"]
                for idx, (video_id, title, uploader) in enumerate(results[:10]):
                    lines.append(f"{idx+1}. {uploader} - {title}")
                await update.message.reply_text("\n".join(lines))
        
        except Exception as e:
            logging.error(f"Ошибка при поиске треков: {e}", exc_info=True)
            await update.message.reply_text(f"❌ Ошибка поиска: {e}")
    
    async def on_callback_select_track(update, context):
        """Обработчик выбора трека из результатов поиска"""
        q = update.callback_query
        await q.answer()
        
        data = q.data or ""
        idx_str = data.split(":", 1)[1] if ":" in data else None
        
        if not idx_str:
            return
        
        try:
            idx = int(idx_str)
        except Exception:
            await q.message.reply_text("❌ Неверный индекс трека")
            return
        
        results = context.chat_data.get("search_results", [])
        if idx < 0 or idx >= len(results):
            await q.message.reply_text("❌ Трек не найден")
            return
        
        video_path = context.chat_data.get("uploaded_video_path")
        if not video_path:
            await q.message.reply_text("⚠️ Видео не найдено. Загрузите видео заново.")
            return
        
        video_id, title, uploader = results[idx]
        
        await q.message.reply_text(f"⏬ Скачиваю трек: {uploader} - {title}")
        
        # Скачиваем выбранный трек
        loop = asyncio.get_running_loop()
        
        def download_track():
            from app.audio import download_specific_track
            import tempfile
            import datetime
            unique_id = datetime.datetime.now().strftime('%Y%m%d_%H%M%S_') + os.urandom(3).hex()
            audio_dir = os.path.join(tempfile.gettempdir(), f"audio_{unique_id}")
            return download_specific_track(video_id, output_dir=audio_dir)
        
        try:
            audio_path = await asyncio.to_thread(download_track)
            
            # Обрабатываем видео с этим аудио
            await process_video_with_selected_audio(update, context, video_path, audio_path)
        
        except Exception as e:
            logging.error(f"Ошибка при скачивании трека: {e}", exc_info=True)
            await q.message.reply_text(f"❌ Ошибка скачивания: {e}")
    
    async def process_video_with_selected_audio(update, context, video_path: str, audio_path: str | None = None, music_playlists: list[str] | None = None):
        """Обрабатывает видео с выбранным аудио"""
        chat_id = update.effective_chat.id
        
        # Убрано сообщение "Начинаю обработку видео..."
        
        loop = asyncio.get_running_loop()
        
        def run_processing():
            def progress(msg: str):
                try:
                    asyncio.run_coroutine_threadsafe(
                        context.bot.send_message(chat_id=chat_id, text=msg),
                        loop
                    ).result(timeout=5)
                except Exception:
                    pass
            
            # Определяем длительность видео, если не передан audio_duration
            video_duration = 12  # значение по умолчанию
            try:
                from app.video import get_video_metadata
                metadata = get_video_metadata(video_path)
                if metadata and metadata.get('duration'):
                    video_duration = int(metadata['duration'])
            except Exception:
                pass
            
            return process_uploaded_video_with_audio(
                video_path=video_path,
                audio_path=audio_path,
                audio_duration=video_duration,
                music_playlists=music_playlists,
                progress=progress
            )
        
        try:
            result = await asyncio.to_thread(run_processing)
            
            if not result or not result.video_path:
                await context.bot.send_message(chat_id=chat_id, text="❌ Не удалось обработать видео")
                return
            
            # Добавляем в историю
            new_item = add_video_history_item(
                result.video_path,
                result.thumbnail_path,
                result.source_url,
                None,
                None
            )
            
            caption = "✅ Видео обработано!\n"
            if result.audio_title:
                caption += f"🎵 Трек: {result.audio_title}\n"
            
            kb = None
            if InlineKeyboardButton and InlineKeyboardMarkup:
                kb = InlineKeyboardMarkup([
                    [InlineKeyboardButton("Опубликовать", callback_data=f"publish:{new_item['id']}")],
                    [InlineKeyboardButton("Выбрать платформы", callback_data=f"choose:{new_item['id']}")],
                    [InlineKeyboardButton("Сменить трек", callback_data=f"changeaudio:{new_item['id']}")],
                ])
            
            # Проверяем что файл существует и не пустой
            if not os.path.exists(result.video_path):
                await context.bot.send_message(chat_id=chat_id, text=f"❌ Файл видео не найден: {result.video_path}")
                return
            
            file_size = os.path.getsize(result.video_path)
            if file_size == 0:
                await context.bot.send_message(chat_id=chat_id, text="❌ Файл видео пустой")
                return
            
            logging.info(f"Отправка видео: {result.video_path} (размер: {file_size} байт)")
            
            try:
                with open(result.video_path, "rb") as video_file:
                    await context.bot.send_video(
                        chat_id=chat_id,
                        video=video_file,
                        caption=caption,
                        reply_markup=kb,
                        read_timeout=60,
                        write_timeout=60,
                        connect_timeout=30
                    )
                logging.info("Видео успешно отправлено")
            except Exception as send_error:
                logging.error(f"Ошибка при отправке видео: {send_error}", exc_info=True)
                await context.bot.send_message(
                    chat_id=chat_id, 
                    text=f"{caption}\n\n⚠️ Не удалось отправить видео напрямую: {send_error}\nФайл сохранён: {result.video_path}", 
                    reply_markup=kb
                )
            
            # Очищаем контекст
            context.chat_data.pop("uploaded_video_path", None)
            context.chat_data.pop("search_results", None)
            context.chat_data.pop("awaiting_audio_upload", None)
            
            # Удаляем загруженное видео
            try:
                if os.path.exists(video_path):
                    os.remove(video_path)
            except Exception:
                pass
        
        except Exception as e:
            logging.error(f"Ошибка при обработке видео: {e}", exc_info=True)
            await context.bot.send_message(chat_id=chat_id, text=f"❌ Ошибка: {e}")
    
    app.add_handler(MessageHandler(filters.VIDEO, on_video_received))
    app.add_handler(MessageHandler(filters.AUDIO | filters.VOICE, on_audio_received))
    app.add_handler(CallbackQueryHandler(on_callback_audio_choice, pattern=r'^audio:'))
    app.add_handler(CallbackQueryHandler(on_callback_select_track, pattern=r'^selecttrack:'))
    app.add_handler(MessageHandler(filters.TEXT & ~filters.COMMAND, on_text_message))
    
    app.add_handler(MessageHandler(filters.Document.ALL, on_document_received))

    async def cmd_scheduleinfo(update, context):
        try:
            set_last_chat_id(update.effective_chat.id)
        except Exception:
            pass
        tz = _tz_tomsk()
        now = datetime.now(tz)
        sched = get_daily_schedule_iso()
        today = [s for s in sched if datetime.fromisoformat(s).date() == now.date()]
        today_dts = []
        for s in today:
            try:
                dt = datetime.fromisoformat(s)
                if dt.tzinfo is None:
                    dt = dt.replace(tzinfo=tz)
                today_dts.append(dt)
            except Exception:
                pass
        today_dts = sorted(today_dts)
        lines = [f"Сейчас: {now.strftime('%d.%m %H:%M:%S')} Asia/Tomsk", f"Всего на сегодня: {len(today_dts)} (окно 10:00–24:00)"]
        for idx, dt in enumerate(today_dts, start=1):
            status = "(прошло)" if dt < now else ""
            lines.append(f"#{idx} {dt.strftime('%H:%M:%S')} {status}")
        if not today_dts:
            lines.append("На сегодня нет расписания — будет сгенерировано автоматически позже")
        await update.message.reply_text("\n".join(lines))

    async def cmd_runscheduled(update, context):
        try:
            set_last_chat_id(update.effective_chat.id)
        except Exception:
            pass
        tz = _tz_tomsk()
        now = datetime.now(tz)
        sched = get_daily_schedule_iso()
        future = []
        for s in sched:
            try:
                dt = datetime.fromisoformat(s)
                if dt.tzinfo is None:
                    dt = dt.replace(tzinfo=tz)
                if dt >= now:
                    future.append(dt)
            except Exception:
                pass
        future = sorted(future)
        if future:
            first = future[0]
            # remove it
            remaining = [d for d in future[1:]]
            past_other = [d for d in [datetime.fromisoformat(s) for s in sched if s not in [f.isoformat() for f in future]] if d.date()!=now.date()]
            combined = [d.isoformat() for d in past_other] + [d.isoformat() for d in remaining]
            set_daily_schedule_iso(combined)
        await update.message.reply_text("Запускаю ближайшую запланированную генерацию сейчас (3 варианта)…")
        pinterest_urls = load_urls_json(DEFAULT_PINTEREST_JSON, [])
        music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
        cleanup_old_temp_dirs()
        async def gen_one_manual(idx:int, attempt:int):
            def run_generation():
                seed = int.from_bytes(os.urandom(4), 'big') ^ (idx + attempt * 7919)
                return generate_meme_video(pinterest_urls, music_playlists, pin_num=10000, audio_duration=10, seed=seed, variant_group=idx % 5)
            return await asyncio.to_thread(run_generation)
        
        gens = []
        for i in range(3):
            result = await gen_one_manual(i, 0)
            gens.append(result)
        def result_source(res):
            return getattr(res,'source_url', None) if res else None
        for attempt in range(1, DUP_REGEN_RETRIES+1):
            seen = set()
            dup_idx = []
            for i,res in enumerate(gens):
                if isinstance(res, Exception) or not res:
                    dup_idx.append(i)
                    continue
                src = result_source(res)
                if not src or src in seen:
                    dup_idx.append(i)
                else:
                    seen.add(src)
            if not dup_idx:
                break
            for i in dup_idx:
                new_res = await gen_one_manual(i, attempt)
                gens[i] = new_res
        results = []
        for res in gens:
            try:
                if isinstance(res, Exception) or not res:
                    continue
                vp = getattr(res, 'video_path', None)
                tp = getattr(res, 'thumbnail_path', None)
                sp = getattr(res, 'source_url', None)
                ap = getattr(res, 'audio_path', None)
                if vp and os.path.exists(vp):
                    it = add_video_history_item(vp, tp, sp, ap)
                    results.append(it)
            except Exception:
                continue
        if not results:
            await update.message.reply_text("Не удалось создать ни одно видео")
            return
        lines = ["Сгенерировано 3 варианта. Выберите для публикации:"]
        kb_rows = []
        for it in results:
            lines.append(f"#{it['id']} {it.get('title')}")
            if InlineKeyboardButton:
                kb_rows.append([InlineKeyboardButton(f"Опубликовать #{it['id']}", callback_data=f"publish:{it['id']}")])
        kb = InlineKeyboardMarkup(kb_rows) if (InlineKeyboardButton and InlineKeyboardMarkup) else None
        try:
            await update.message.reply_text("\n".join(lines))
        except Exception:
            pass
        for it in results:
            if not it:
                continue
            vid = it['id']
            kb2 = None
            if InlineKeyboardButton and InlineKeyboardMarkup:
                kb2 = InlineKeyboardMarkup([
                    [InlineKeyboardButton(f"Опубликовать #{vid}", callback_data=f"publish:{vid}")],
                    [InlineKeyboardButton(f"Выбрать платформы #{vid}", callback_data=f"choose:{vid}")],
                    [InlineKeyboardButton(f"Сменить трек #{vid}", callback_data=f"changeaudio:{vid}")],
                ])
            try:
                info_text = f"Кандидат #{vid}\n{_format_video_info_from_history(it)}"
                await update.message.reply_video(video=open(it['video_path'],'rb'), caption=info_text, reply_markup=kb2)
            except Exception:
                try:
                    info_text = f"Кандидат #{vid}\n{_format_video_info_from_history(it)}"
                    await update.message.reply_text(info_text, reply_markup=kb2)
                except Exception:
                    pass

    app.add_handler(CommandHandler("scheduleinfo", cmd_scheduleinfo))
    app.add_handler(CommandHandler("runscheduled", cmd_runscheduled))

    async def cmd_rebuildschedule(update, context):
        tz = _tz_tomsk()
        now = datetime.now(tz)
        times = _build_daily_schedule_for_date(now.date(), DAILY_GENERATIONS)
        # preserve future non-today schedules (none usually) but we overwrite today
        other = [s for s in get_daily_schedule_iso() if datetime.fromisoformat(s).date() != now.date()]
        set_daily_schedule_iso(other + [t.isoformat() for t in times])
        # reschedule next
        future = [t for t in times if t > now]
        if future:
            _reschedule_to(context.application, future[0])
        today_dts = sorted(times)
        lines = [
            f"Сейчас: {now.strftime('%d.%m %H:%M:%S')} Asia/Tomsk",
            f"Всего на сегодня: {len(today_dts)} (окно 10:00–24:00)",
        ]
        for idx, dt in enumerate(today_dts, start=1):
            status = "(прошло)" if dt < now else ""
            lines.append(f"#{idx} {dt.strftime('%H:%M:%S')} {status}")
        if not today_dts:
            lines.append("На сегодня нет расписания — будет сгенерировано автоматически позже")
        await update.message.reply_text("\n".join(lines))

    app.add_handler(CommandHandler("rebuildschedule", cmd_rebuildschedule))

    async def cmd_setnext(update, context):
        tz = _tz_tomsk()
        args = context.args or []
        if len(args) < 2:
            await update.message.reply_text("Использование: /setnext <index> <HH:MM | +30m | +2h | YYYY-MM-DD HH:MM>")
            return
        try:
            idx = int(args[0])
        except Exception:
            await update.message.reply_text("Первый параметр должен быть индексом (#) из /scheduleinfo")
            return
        raw = " ".join(args[1:]).strip().replace("T", " ")
        sched = get_daily_schedule_iso()
        tznow = datetime.now(tz)
        today_sched = [s for s in sched if datetime.fromisoformat(s).date() == tznow.date()]
        today_sched_dt = []
        for s in today_sched:
            dt = datetime.fromisoformat(s)
            if dt.tzinfo is None:
                dt = dt.replace(tzinfo=tz)
            today_sched_dt.append(dt)
        today_sched_dt = sorted(today_sched_dt)
        if idx < 1 or idx > len(today_sched_dt):
            await update.message.reply_text("Неверный индекс")
            return
        target = None
        base_dt = today_sched_dt[idx - 1]
        try:
            if (raw.startswith('+') or raw.startswith('-')) and len(raw) >= 3:
                sign = -1 if raw[0] == '-' else 1
                num = ''.join(ch for ch in raw[1:] if ch.isdigit())
                unit = raw[-1].lower()
                val = int(num) if num else 0
                delta = timedelta(0)
                if unit == 'm':
                    delta = timedelta(minutes=val * sign)
                elif unit == 'h':
                    delta = timedelta(hours=val * sign)
                elif unit == 'd':
                    delta = timedelta(days=val * sign)
                if delta != timedelta(0):
                    target = base_dt + delta
            if target is None:
                try:
                    target = datetime.fromisoformat(raw)
                    if target.tzinfo is None:
                        target = target.replace(tzinfo=tz)
                except Exception:
                    pass
            if target is None and ':' in raw and len(raw) <= 5:
                hh, mm = raw.split(':', 1)
                target = base_dt.replace(hour=int(hh), minute=int(mm), second=0, microsecond=0)
            if target is None:
                await update.message.reply_text("Не удалось распарсить время")
                return
            if target < tznow:
                await update.message.reply_text("Нельзя установить время в прошлом")
                return
            # apply
            new_list = []
            for dt in today_sched_dt:
                if dt == base_dt:
                    new_list.append(target)
                else:
                    new_list.append(dt)
            new_list = sorted(new_list)
            # merge back with non-today
            other = [s for s in sched if datetime.fromisoformat(s).date() != tznow.date()]
            set_daily_schedule_iso([*other, *[d.isoformat() for d in new_list]])
            # reschedule if needed
            next_future = [d for d in new_list if d > tznow]
            if next_future:
                if context.application.bot_data.get('scheduled_target_iso'):
                    cur_iso = context.application.bot_data.get('scheduled_target_iso')
                    try:
                        cur_dt = datetime.fromisoformat(cur_iso)
                        if cur_dt.tzinfo is None:
                            cur_dt = cur_dt.replace(tzinfo=tz)
                    except Exception:
                        cur_dt = None
                else:
                    cur_dt = None
                soonest = min(next_future)
                if cur_dt is None or soonest != cur_dt:
                    _reschedule_to(context.application, soonest)
            await update.message.reply_text("Обновлено. /scheduleinfo для просмотра.")
        except Exception as e:
            await update.message.reply_text(f"Ошибка: {e}")
    async def cmd_chatid(update, context):
        cid = update.effective_chat.id
        try:
            set_selected_chat_id(cid)
            set_last_chat_id(cid)
        except Exception:
            pass
        await update.message.reply_text(f"Chat ID: {cid} сохранён")

    app.add_handler(CommandHandler("chatid", cmd_chatid))

    app.add_handler(CommandHandler("setnext", cmd_setnext))

    async def on_error(update, context):
        err = getattr(context, 'error', None)
        logging.exception("Unhandled exception in Telegram handler", exc_info=True)
        if err is None:
            return
        try:
            try:
                if tgerr is not None and isinstance(err, (tgerr.NetworkError, tgerr.TimedOut)):
                    return
            except Exception:
                pass
            if httpx is not None and isinstance(err, (httpx.ReadError, )):
                return
            if 'ReadError' in repr(err):
                return
        except Exception:
            pass
        try:
            save_generation_state("bot_error", {"error": str(err), "type": err.__class__.__name__, "time": datetime.now(_tz_tomsk()).isoformat()})
        except Exception:
            pass
        try:
            now_ts = time.time()
            last_ts = context.application.bot_data.get('last_error_notify_ts', 0)
            if now_ts - last_ts < 60:
                return
            context.application.bot_data['last_error_notify_ts'] = now_ts
            cid = _resolve_notify_chat_id()
            if cid:
                msg = f"⚠️ Telegram error: {err.__class__.__name__}: {err}"
                try:
                    await context.bot.send_message(chat_id=cid, text=msg)
                except Exception:
                    pass
        except Exception:
            pass

    app.add_error_handler(on_error)

    print("Бот запущен. Нажмите Ctrl+C для остановки.")
    logging.info("Бот запущен и готов к работе")
    app.run_polling()


if __name__ == "__main__":
    main()
