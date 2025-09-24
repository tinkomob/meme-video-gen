import asyncio
import os
import json
import time
import logging
from typing import Optional
from datetime import datetime, timedelta
from zoneinfo import ZoneInfo
from io import BytesIO

from dotenv import load_dotenv
from app.config import TELEGRAM_BOT_TOKEN, DEFAULT_THUMBNAIL, HISTORY_FILE
from app.service import generate_meme_video, deploy_to_socials
from app.utils import load_urls_json, replace_file_from_bytes, clear_video_history, read_small_file
from app.history import add_video_history_item, load_video_history, save_video_history
from app.config import TIKTOK_COOKIES_FILE, CLIENT_SECRETS, TOKEN_PICKLE, YT_COOKIES_FILE
from app.state import set_last_chat_id, get_last_chat_id, set_next_run_iso, get_next_run_iso, set_daily_schedule_iso, get_daily_schedule_iso, set_selected_chat_id, get_selected_chat_id
from app.config import DAILY_GENERATIONS

try:
    from telegram import InlineKeyboardButton, InlineKeyboardMarkup
except Exception:
    InlineKeyboardButton = None
    InlineKeyboardMarkup = None

load_dotenv()

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
ALL_SOCIALS = ["youtube", "instagram", "tiktok", "x"]


HELP_TEXT = (
    "Команды:\n"
    "/start — приветствие\n"
    "/help — помощь\n"
    "/generate — сгенерировать видео (опционально: кол-во пинов и длительность аудио)\n"
    "/deploy — опубликовать последнее видео (опционально: соцсети, приватность, dry)\n"
    "/dryrun — показать/изменить режим публикации (on/off)\n"
    "/checkfiles — проверить cookies.txt, youtube_cookies.txt, client_secrets.json, token.pickle и instagram_session.json\n"
    "/history — последние публикации\n"
    "/uploadcookies — загрузить cookies.txt (TikTok) как документ\n"
    "/uploadytcookies — загрузить youtube_cookies.txt (YouTube) как документ\n"
    "/uploadinstasession — загрузить instagram_session.json (Instagram) как документ\n"
    "/uploadclient — загрузить client_secrets.json как документ\n"
    "/uploadtoken — загрузить token.pickle как документ\n"
    "/clearhistory — очистить video_history.json\n"
    "/scheduleinfo — показать расписание всех генераций на сегодня\n"
    "/runscheduled — немедленно выполнить ближайшую запланированную генерацию\n"
    "/setnext — изменить время запланированной генерации: /setnext <index> <время|сдвиг> (пример: /setnext 2 22:10, /setnext 1 +30m)\n"
    "/chatid — показать и сохранить текущий chat id"
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


async def cmd_generate(update, context):
    try:
        set_last_chat_id(update.effective_chat.id)
    except Exception:
        pass
    args = context.args or []
    pin_num = parse_int(args[0], 50) if len(args) >= 1 else 10000
    audio_duration = parse_int(args[1], 10) if len(args) >= 2 else 10

    pinterest_urls = load_urls_json(DEFAULT_PINTEREST_JSON, [])
    music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
    reddit_sources = load_urls_json(DEFAULT_REDDIT_JSON, [])

    if not pinterest_urls and not music_playlists:
        await update.message.reply_text("Нет источников. Добавьте ссылки в pinterest_urls.json и music_playlists.json")
        return
    context.chat_data["gen_msg_ids"] = []
    context.chat_data.pop("progress_msg_id", None)
    context.chat_data.pop("progress_lines", None)
    start_msg = await _progress_init(context, update.effective_chat.id, f"Запускаю генерацию... pins={pin_num}, audio={audio_duration}s")

    loop = asyncio.get_running_loop()

    def run_generation():
        def progress(msg: str):
            asyncio.run_coroutine_threadsafe(_progress_queue(context, update.effective_chat.id, msg), loop)
        result = generate_meme_video(
            pinterest_urls=pinterest_urls,
            music_playlists=music_playlists,
            pin_num=pin_num,
            audio_duration=audio_duration,
            progress=progress,
            reddit_sources=reddit_sources,
        )
        return result

    result = await asyncio.to_thread(run_generation)

    if not result or not result.video_path:
        await update.message.reply_text("Не удалось создать видео.")
        return

    new_item = add_video_history_item(result.video_path, result.thumbnail_path, result.source_url, result.audio_path)

    caption = f"Готово.\nИсточник: {result.source_url or '-'}"

    kb = None
    if InlineKeyboardButton and InlineKeyboardMarkup:
        kb = InlineKeyboardMarkup(
            [
                [InlineKeyboardButton("Опубликовать", callback_data=f"publish:{new_item['id']}")],
                [InlineKeyboardButton("Выбрать платформы", callback_data=f"choose:{new_item['id']}")],
                [InlineKeyboardButton("Сгенерировать заново", callback_data=f"regenerate:{new_item['id']}")],
            ]
        )

    try:
        m = await update.message.reply_video(
            video=open(result.video_path, "rb"),
            caption=caption,
            reply_markup=kb,
        )
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

    await update.message.reply_text("Публикую... Это может занять несколько минут.")

    loop = asyncio.get_running_loop()

    def run_deploy():
        def progress(msg: str):
            asyncio.run_coroutine_threadsafe(
                update.message.reply_text(msg), loop
            )
        links = deploy_to_socials(
            video_path=video_path,
            thumbnail_path=thumbnail_path,
            source_url=source_url or "",
            audio_path=audio_path,
            privacy=privacy,
            socials=socials,
            dry_run=_get_bot_dry_run(context) if dry_run_opt is None else dry_run_opt,
            progress=progress,
        )
        return links

    links = await asyncio.to_thread(run_deploy)

    text_lines = ["Готово:"]
    for k, v in (links or {}).items():
        text_lines.append(f"- {k}: {v or '—'}")

    await update.message.reply_text("\n".join(text_lines))


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

    await q.message.reply_text("Публикую… Подождите, это займёт несколько минут.")

    loop = asyncio.get_running_loop()

    def run_deploy():
        def progress(msg: str):
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
        )

    links = await asyncio.to_thread(run_deploy)
    if isinstance(links, dict):
        for it in hist:
            if str(it.get("id")) == str(item_id):
                it["deployment_links"] = links
                break
        save_video_history(hist)
    lines = ["Готово:"]
    for k, v in (links or {}).items():
        lines.append(f"- {k}: {v or '—'}")
    await q.message.reply_text("\n".join(lines))


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
            InlineKeyboardButton(label("tiktok"), callback_data=f"toggle:tiktok:{item_id}"),
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
    await q.message.reply_text("Публикую выбранные платформы…")
    loop = asyncio.get_running_loop()
    def run_deploy():
        def progress(msg: str):
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
        )
    links = await asyncio.to_thread(run_deploy)
    if isinstance(links, dict):
        for it in hist:
            if str(it.get("id")) == str(item_id):
                it["deployment_links"] = links
                break
        save_video_history(hist)
    lines = ["Готово:"]
    for k, v in (links or {}).items():
        lines.append(f"- {k}: {v or '—'}")
    await q.message.reply_text("\n".join(lines))


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
    await q.message.reply_text("Публикую во все платформы…")
    loop = asyncio.get_running_loop()
    def run_deploy():
        def progress(msg: str):
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
        )
    links = await asyncio.to_thread(run_deploy)
    if isinstance(links, dict):
        for it in hist:
            if str(it.get("id")) == str(item_id):
                it["deployment_links"] = links
                break
        save_video_history(hist)
    lines = ["Готово:"]
    for k, v in (links or {}).items():
        lines.append(f"- {k}: {v or '—'}")
    await q.message.reply_text("\n".join(lines))


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
        )

    result = await asyncio.to_thread(run_generation)
    if not result or not result.video_path:
        await q.message.reply_text("Не удалось создать новое видео.")
        return
    new_item = add_video_history_item(result.video_path, result.thumbnail_path, result.source_url, result.audio_path)
    caption = f"Готово.\nИсточник: {result.source_url or '-'}"
    kb = None
    if InlineKeyboardButton and InlineKeyboardMarkup:
        kb = InlineKeyboardMarkup(
            [
                [InlineKeyboardButton("Опубликовать", callback_data=f"publish:{new_item['id']}")],
                [InlineKeyboardButton("Выбрать платформы", callback_data=f"choose:{new_item['id']}")],
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


async def cmd_uploadcookies(update, context):
    doc = getattr(update.message, "document", None)
    if not doc:
        context.chat_data["await_upload"] = "cookies"
        await update.message.reply_text("Пришлите файл cookies.txt как документ следующим сообщением (ожидаю cookies)")
        return
    try:
        file = await context.bot.get_file(doc.file_id)
        data = await file.download_as_bytearray()
        ok = replace_file_from_bytes(TIKTOK_COOKIES_FILE, bytes(data))
        await update.message.reply_text("cookies.txt обновлён" if ok else "Не удалось сохранить cookies.txt")
    except Exception as e:
        await update.message.reply_text(f"Ошибка: {e}")

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


async def cmd_uploadinstasession(update, context):
    doc = getattr(update.message, "document", None)
    if not doc:
        context.chat_data["await_upload"] = "instasession"
        await update.message.reply_text("Пришлите instagram_session.json как документ следующим сообщением (ожидаю Instagram session)")
        return
    try:
        file = await context.bot.get_file(doc.file_id)
        data = await file.download_as_bytearray()
        ok = replace_file_from_bytes("instagram_session.json", bytes(data))
        await update.message.reply_text("instagram_session.json обновлён" if ok else "Не удалось сохранить instagram_session.json")
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
        "TikTok cookies.txt": TIKTOK_COOKIES_FILE,
        "YouTube youtube_cookies.txt": YT_COOKIES_FILE,
        "YouTube client_secrets.json": CLIENT_SECRETS,
        "YouTube token.pickle": TOKEN_PICKLE,
        "Instagram instagram_session.json": "instagram_session.json",
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
    if not os.path.exists(TIKTOK_COOKIES_FILE):
        lines.append("Загрузите cookies.txt командой /uploadcookies (пришлите файл как документ)")
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
    if not os.path.exists("instagram_session.json"):
        lines.append("Для Instagram загрузите instagram_session.json командой /uploadinstasession")
    await update.message.reply_text("\n".join(lines))


async def on_document_received(update, context):
    doc = getattr(update.message, "document", None)
    if not doc:
        return
    purpose = context.chat_data.pop("await_upload", None)
    fname = (getattr(doc, "file_name", "") or "").lower()
    target = None
    if purpose == "cookies" or fname == "cookies.txt" or fname.endswith("/cookies.txt"):
        target = TIKTOK_COOKIES_FILE
    elif purpose == "ytcookies" or fname == "youtube_cookies.txt" or fname.endswith("/youtube_cookies.txt"):
        target = YT_COOKIES_FILE
    elif purpose == "client" or fname == "client_secrets.json" or fname.endswith("/client_secrets.json"):
        target = CLIENT_SECRETS
    elif purpose == "token" or fname == "token.pickle" or fname.endswith("/token.pickle"):
        target = TOKEN_PICKLE
    elif purpose == "instasession" or fname == "instagram_session.json" or fname.endswith("/instagram_session.json"):
        target = "instagram_session.json"
    else:
        if fname.endswith("cookies.txt"):
            target = TIKTOK_COOKIES_FILE
        elif fname.endswith("youtube_cookies.txt"):
            target = YT_COOKIES_FILE
        elif fname.endswith("client_secrets.json"):
            target = CLIENT_SECRETS
        elif fname.endswith("token.pickle"):
            target = TOKEN_PICKLE
        elif fname.endswith("instagram_session.json"):
            target = "instagram_session.json"
    if not target:
        await update.message.reply_text("Неизвестный файл. Ожидаю cookies.txt, youtube_cookies.txt, client_secrets.json, token.pickle или instagram_session.json")
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
    tz = ZoneInfo("Asia/Tomsk")
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
    tz = ZoneInfo("Asia/Tomsk")
    start = datetime(year=date_obj.year, month=date_obj.month, day=date_obj.day, hour=10, minute=0, second=0, microsecond=0, tzinfo=tz)
    # exclusive midnight next day
    end = (start + timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0) + timedelta(days=0)
    total_seconds = int((end - start).total_seconds())
    if total_seconds <= 0:
        total_seconds = 12 * 3600
    offset = int(os.urandom(2).hex(), 16) % total_seconds
    return start + timedelta(seconds=offset)


def _compute_next_target(now: datetime) -> datetime:
    tz = ZoneInfo("Asia/Tomsk")
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
    tz = ZoneInfo("Asia/Tomsk")
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
    tz = ZoneInfo("Asia/Tomsk")
    tznow = datetime.now(tz)
    schedule_list = get_daily_schedule_iso()
    if schedule_list:
        schedule_list = sorted(schedule_list)
        # remove past
        schedule_list = [x for x in schedule_list if datetime.fromisoformat(x) > tznow]
        set_daily_schedule_iso(schedule_list)
    pinterest_urls = load_urls_json(DEFAULT_PINTEREST_JSON, [])
    music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
    reddit_sources = load_urls_json(DEFAULT_REDDIT_JSON, [])
    cid = _resolve_notify_chat_id()
    if not cid:
        logging.warning("Не найден chat_id для уведомления")
    # generate 3 candidates
    results = []
    for i in range(3):
        def run_generation():
            return generate_meme_video(pinterest_urls, music_playlists, pin_num=10000, audio_duration=10, reddit_sources=reddit_sources)
        res = await asyncio.to_thread(run_generation)
        if res and res.video_path and os.path.exists(res.video_path):
            item = add_video_history_item(res.video_path, res.thumbnail_path, res.source_url, res.audio_path)
            results.append(item)
        else:
            results.append(None)
        try:
            await asyncio.sleep(0.5 + (i * 0.3))
        except Exception:
            pass
    if cid and results:
        lines = ["Автогенерация завершена. Отправлено 3 варианта ниже:"]
        try:
            await app.bot.send_message(chat_id=cid, text="\n".join(lines))
        except Exception:
            pass
        for item in results:
            if not item:
                continue
            vid = item['id']
            kb = None
            if InlineKeyboardButton and InlineKeyboardMarkup:
                kb = InlineKeyboardMarkup([[InlineKeyboardButton(f"Опубликовать #{vid}", callback_data=f"publish:{vid}")],[InlineKeyboardButton(f"Выбрать платформы #{vid}", callback_data=f"choose:{vid}")]])
            try:
                if item.get('video_path') and os.path.exists(item.get('video_path')):
                    await app.bot.send_video(chat_id=cid, video=open(item.get('video_path'), 'rb'), caption=f"Кандидат #{vid}", reply_markup=kb)
                else:
                    await app.bot.send_message(chat_id=cid, text=f"Кандидат #{vid}", reply_markup=kb)
            except Exception:
                pass
    # schedule next remaining today or generate tomorrow set
    schedule_list = get_daily_schedule_iso()
    tznow2 = datetime.now(tz)
    future = [datetime.fromisoformat(x) for x in schedule_list if datetime.fromisoformat(x) > tznow2]
    if future:
        next_dt = min(future)
        _reschedule_to(app, next_dt)
        app.bot_data['scheduled_target_iso'] = next_dt.isoformat()
    else:
        # build tomorrow schedule
        tomorrow = (tznow2 + timedelta(days=1)).date()
        times = _build_daily_schedule_for_date(tomorrow, DAILY_GENERATIONS)
        set_daily_schedule_iso([t.isoformat() for t in times])
        next_dt = times[0]
        _reschedule_to(app, next_dt)
        app.bot_data['scheduled_target_iso'] = next_dt.isoformat()
    logging.info(f"Сформировано следующее расписание: {get_daily_schedule_iso()}")


def _build_daily_schedule_for_date(date_obj, count: int) -> list[datetime]:
    tz = ZoneInfo("Asia/Tomsk")
    start = datetime(year=date_obj.year, month=date_obj.month, day=date_obj.day, hour=10, minute=0, second=0, microsecond=0, tzinfo=tz)
    # exclusive midnight: next day 00:00
    end = (start + timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0)
    total_seconds = int((end - start).total_seconds())
    if count <= 1:
        return [start + timedelta(seconds=int(total_seconds/2))]
    segment = total_seconds / count
    times = []
    for i in range(count):
        seg_start = start + timedelta(seconds=int(i * segment))
        seg_end = start + timedelta(seconds=int((i + 1) * segment))
        span = int((seg_end - seg_start).total_seconds())
        if span <= 0:
            span = 60
        jitter = int(os.urandom(2).hex(), 16) % span
        t = seg_start + timedelta(seconds=jitter)
        if t < seg_start:
            t = seg_start
        if t > seg_end:
            t = seg_end - timedelta(seconds=1)
        times.append(t)
    times = sorted(times)
    return times

def _schedule_next_job(app):
    logging.info("Планирование списка ежедневных генераций")
    tz = ZoneInfo("Asia/Tomsk")
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
                    await appinst.bot.send_message(chat_id=cid, text="Бот перезапущен. Планировщик активен.")
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
            async def watchdog():
                tz = ZoneInfo("Asia/Tomsk")
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
    app.add_handler(CommandHandler("generate", cmd_generate))
    app.add_handler(CommandHandler("deploy", cmd_deploy))
    app.add_handler(CommandHandler("history", cmd_history))
    app.add_handler(CommandHandler("uploadcookies", cmd_uploadcookies))
    app.add_handler(CommandHandler("uploadytcookies", cmd_uploadytcookies))
    app.add_handler(CommandHandler("uploadinstasession", cmd_uploadinstasession))
    app.add_handler(CommandHandler("uploadclient", cmd_uploadclient))
    app.add_handler(CommandHandler("uploadtoken", cmd_uploadtoken))
    app.add_handler(CommandHandler("clearhistory", cmd_clearhistory))
    app.add_handler(CommandHandler("checkfiles", cmd_checkfiles))
    app.add_handler(CallbackQueryHandler(on_callback_publish, pattern=r"^publish:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_choose_platforms, pattern=r"^choose:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_toggle_platform, pattern=r"^toggle:[A-Za-z]+:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_publish_selected, pattern=r"^publishsel:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_publish_all, pattern=r"^publishall:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_cancel_choose, pattern=r"^cancelchoose:\d+"))
    app.add_handler(CallbackQueryHandler(on_callback_regenerate, pattern=r"^regenerate:\d+"))

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
    app.add_handler(MessageHandler(filters.Document.ALL, on_document_received))

    async def cmd_scheduleinfo(update, context):
        try:
            set_last_chat_id(update.effective_chat.id)
        except Exception:
            pass
        tz = ZoneInfo("Asia/Tomsk")
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
        tz = ZoneInfo("Asia/Tomsk")
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
        reddit_sources = load_urls_json(DEFAULT_REDDIT_JSON, [])
        results = []
        for i in range(3):
            def run_generation():
                return generate_meme_video(pinterest_urls, music_playlists, pin_num=10000, audio_duration=10, reddit_sources=reddit_sources)
            res = await asyncio.to_thread(run_generation)
            if res and res.video_path and os.path.exists(res.video_path):
                it = add_video_history_item(res.video_path, res.thumbnail_path, res.source_url, res.audio_path)
                results.append(it)
            try:
                await asyncio.sleep(0.5 + (i * 0.3))
            except Exception:
                pass
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
            vid = it['id']
            kb2 = None
            if InlineKeyboardButton and InlineKeyboardMarkup:
                kb2 = InlineKeyboardMarkup([[InlineKeyboardButton(f"Опубликовать #{vid}", callback_data=f"publish:{vid}")],[InlineKeyboardButton(f"Выбрать платформы #{vid}", callback_data=f"choose:{vid}")]])
            try:
                await update.message.reply_video(video=open(it['video_path'],'rb'), caption=f"Кандидат #{vid}", reply_markup=kb2)
            except Exception:
                try:
                    await update.message.reply_text(f"Кандидат #{vid}", reply_markup=kb2)
                except Exception:
                    pass

    app.add_handler(CommandHandler("scheduleinfo", cmd_scheduleinfo))
    app.add_handler(CommandHandler("runscheduled", cmd_runscheduled))

    async def cmd_setnext(update, context):
        tz = ZoneInfo("Asia/Tomsk")
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

    print("Бот запущен. Нажмите Ctrl+C для остановки.")
    logging.info("Бот запущен и готов к работе")
    app.run_polling()


if __name__ == "__main__":
    main()
