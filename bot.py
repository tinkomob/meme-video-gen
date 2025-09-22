import asyncio
import os
import json
import time
from typing import Optional
from datetime import datetime, timedelta
from zoneinfo import ZoneInfo
from io import BytesIO

from dotenv import load_dotenv
from app.config import TELEGRAM_BOT_TOKEN, DEFAULT_THUMBNAIL, HISTORY_FILE
from app.service import generate_meme_video, deploy_to_socials
from app.utils import load_urls_json, replace_file_from_bytes, clear_video_history, read_small_file
from app.history import add_video_history_item, load_video_history, save_video_history
from app.config import TIKTOK_COOKIES_FILE, CLIENT_SECRETS, TOKEN_PICKLE
from app.state import set_last_chat_id, get_last_chat_id, set_next_run_iso, get_next_run_iso

try:
    from telegram import InlineKeyboardButton, InlineKeyboardMarkup
except Exception:
    InlineKeyboardButton = None
    InlineKeyboardMarkup = None

load_dotenv()

DEFAULT_PINTEREST_JSON = "pinterest_urls.json"
DEFAULT_PLAYLISTS_JSON = "music_playlists.json"
ALL_SOCIALS = ["youtube", "instagram", "tiktok", "x"]


HELP_TEXT = (
    "Команды:\n"
    "/start — приветствие\n"
    "/help — помощь\n"
    "/generate — сгенерировать видео (опционально: кол-во пинов и длительность аудио)\n"
    "/deploy — опубликовать последнее видео (опционально: соцсети, приватность, dry)\n"
    "/dryrun — показать/изменить режим публикации (on/off)\n"
    "/history — последние публикации\n"
    "/uploadcookies — загрузить cookies.txt (TikTok) как документ\n"
    "/uploadclient — загрузить client_secrets.json как документ\n"
    "/uploadtoken — загрузить token.pickle как документ\n"
    "/clearhistory — очистить video_history.json\n"
    "/scheduleinfo — показать расписание\n"
    "/runscheduled — немедленно выполнить запланированное (ручной старт)"
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
        )
        return result

    result = await asyncio.to_thread(run_generation)

    if not result or not result.video_path:
        await update.message.reply_text("Не удалось создать видео.")
        return

    new_item = add_video_history_item(result.video_path, result.thumbnail_path, result.source_url, result.audio_path)

    caption = f"Готово. Видео: {result.video_path}\nИсточник: {result.source_url or '-'}"

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
        )

    result = await asyncio.to_thread(run_generation)
    if not result or not result.video_path:
        await q.message.reply_text("Не удалось создать новое видео.")
        return
    new_item = add_video_history_item(result.video_path, result.thumbnail_path, result.source_url)
    caption = f"Готово. Видео: {result.video_path}\nИсточник: {result.source_url or '-'}"
    kb = None
    if InlineKeyboardButton and InlineKeyboardMarkup:
        kb = InlineKeyboardMarkup(
            [
                [InlineKeyboardButton("Опубликовать", callback_data=f"publish:{new_item['id']}")],
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


async def on_document_received(update, context):
    doc = getattr(update.message, "document", None)
    if not doc:
        return
    purpose = context.chat_data.pop("await_upload", None)
    fname = (getattr(doc, "file_name", "") or "").lower()
    target = None
    if purpose == "cookies" or fname == "cookies.txt" or fname.endswith("/cookies.txt"):
        target = TIKTOK_COOKIES_FILE
    elif purpose == "client" or fname == "client_secrets.json" or fname.endswith("/client_secrets.json"):
        target = CLIENT_SECRETS
    elif purpose == "token" or fname == "token.pickle" or fname.endswith("/token.pickle"):
        target = TOKEN_PICKLE
    else:
        if fname.endswith("cookies.txt"):
            target = TIKTOK_COOKIES_FILE
        elif fname.endswith("client_secrets.json"):
            target = CLIENT_SECRETS
        elif fname.endswith("token.pickle"):
            target = TOKEN_PICKLE
    if not target:
        await update.message.reply_text("Неизвестный файл. Ожидаю cookies.txt, client_secrets.json или token.pickle")
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
    end = now.replace(hour=22, minute=0, second=0, microsecond=0)
    if now >= end:
        start = (now + timedelta(days=1)).replace(hour=10, minute=0, second=0, microsecond=0)
        end = (now + timedelta(days=1)).replace(hour=22, minute=0, second=0, microsecond=0)
    total_seconds = int((end - start).total_seconds())
    if total_seconds <= 0:
        total_seconds = 12 * 3600
    offset = int(os.urandom(2).hex(), 16) % total_seconds
    return start + timedelta(seconds=offset)


async def _scheduled_worker(app):
    tz = ZoneInfo("Asia/Tomsk")
    while True:
        try:
            target_dt = _random_time_today_tomsk()
            try:
                set_next_run_iso(target_dt.isoformat())
            except Exception:
                pass
            now = datetime.now(tz)
            delay = (target_dt - now).total_seconds()
            if delay < 1:
                delay = 1
            try:
                cid = get_last_chat_id()
                if cid:
                    try:
                        await app.bot.send_message(chat_id=cid, text=f"Следующая авто-генерация запланирована на {target_dt.strftime('%d.%m %H:%M')} (Asia/Tomsk)")
                    except Exception:
                        pass
            except Exception:
                pass
            await asyncio.sleep(delay)

            pinterest_urls = load_urls_json(DEFAULT_PINTEREST_JSON, [])
            music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
            def run_generation():
                return generate_meme_video(pinterest_urls, music_playlists, pin_num=10000, audio_duration=10)
            res = await asyncio.to_thread(run_generation)
            cid = get_last_chat_id()
            if cid:
                if res and res.video_path and os.path.exists(res.video_path):
                    caption = "Автогенерация завершена. Отправить в платформы?"
                    kb = None
                    if InlineKeyboardButton and InlineKeyboardMarkup:
                        items = add_video_history_item(res.video_path, res.thumbnail_path, res.source_url, res.audio_path)
                        kb = InlineKeyboardMarkup([[InlineKeyboardButton("Опубликовать", callback_data=f"publish:{items['id']}")],[InlineKeyboardButton("Выбрать платформы", callback_data=f"choose:{items['id']}")]])
                    try:
                        await app.bot.send_video(chat_id=cid, video=open(res.video_path, 'rb'), caption=caption, reply_markup=kb)
                    except Exception:
                        try:
                            await app.bot.send_message(chat_id=cid, text=f"Видео готово: {res.video_path}. Отправить в платформы командой /deploy")
                        except Exception:
                            pass
                else:
                    try:
                        await app.bot.send_message(chat_id=cid, text="Автогенерация не удалась")
                    except Exception:
                        pass
        except Exception:
            try:
                await asyncio.sleep(60)
            except Exception:
                pass


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
        try:
            cid = get_last_chat_id()
            if cid:
                try:
                    await appinst.bot.send_message(chat_id=cid, text="Бот перезапущен. Планировщик активен.")
                except Exception:
                    pass
        except Exception:
            pass
        appinst.create_task(_scheduled_worker(appinst))

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
    app.add_handler(CommandHandler("uploadclient", cmd_uploadclient))
    app.add_handler(CommandHandler("uploadtoken", cmd_uploadtoken))
    app.add_handler(CommandHandler("clearhistory", cmd_clearhistory))
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
        tz = ZoneInfo("Asia/Tomsk")
        now = datetime.now(tz)
        nxt = get_next_run_iso()
        if nxt:
            try:
                nxt_dt = datetime.fromisoformat(nxt)
            except Exception:
                nxt_dt = None
        else:
            nxt_dt = None
        if nxt_dt is None:
            msg = f"Сейчас: {now.strftime('%d.%m %H:%M')} Asia/Tomsk. Расписание: случайное время 10:00–22:00 ежедневно. Следующее время пока не определено — ожидайте планировщик."
        else:
            if nxt_dt.tzinfo is None:
                nxt_dt = nxt_dt.replace(tzinfo=tz)
            delta = max(0, int((nxt_dt - now).total_seconds()))
            hh = delta // 3600
            mm = (delta % 3600) // 60
            ss = delta % 60
            msg = (
                f"Сейчас: {now.strftime('%d.%m %H:%M:%S')} Asia/Tomsk\n"
                f"Следующая автогенерация: {nxt_dt.strftime('%d.%m %H:%M:%S')} Asia/Tomsk\n"
                f"Осталось: {hh:02d}:{mm:02d}:{ss:02d}.\n"
                f"Правило: ежедневно в случайное время 10:00–22:00."
            )
        await update.message.reply_text(msg)

    async def cmd_runscheduled(update, context):
        await update.message.reply_text("Запускаю внеплановую генерацию…")
        pinterest_urls = load_urls_json(DEFAULT_PINTEREST_JSON, [])
        music_playlists = load_urls_json(DEFAULT_PLAYLISTS_JSON, [])
        def run_generation():
            return generate_meme_video(pinterest_urls, music_playlists, pin_num=10000, audio_duration=10)
        res = await asyncio.to_thread(run_generation)
        if res and res.video_path and os.path.exists(res.video_path):
            it = add_video_history_item(res.video_path, res.thumbnail_path, res.source_url, res.audio_path)
            kb = None
            if InlineKeyboardButton and InlineKeyboardMarkup:
                kb = InlineKeyboardMarkup([[InlineKeyboardButton("Опубликовать", callback_data=f"publish:{it['id']}")]])
            try:
                await update.message.reply_video(video=open(res.video_path, 'rb'), caption="Готово", reply_markup=kb)
            except Exception:
                await update.message.reply_text(f"Видео: {res.video_path}")
        else:
            await update.message.reply_text("Не удалось создать видео")

    app.add_handler(CommandHandler("scheduleinfo", cmd_scheduleinfo))
    app.add_handler(CommandHandler("runscheduled", cmd_runscheduled))

    print("Бот запущен. Нажмите Ctrl+C для остановки.")
    app.run_polling()


if __name__ == "__main__":
    main()
