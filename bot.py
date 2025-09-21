import asyncio
import os
import json
import time
from typing import Optional

from dotenv import load_dotenv
from app.config import TELEGRAM_BOT_TOKEN, DEFAULT_THUMBNAIL, HISTORY_FILE
from app.service import generate_meme_video, deploy_to_socials
from app.utils import load_urls_json
from app.history import add_video_history_item, load_video_history, save_video_history

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
    "/history — последние публикации"
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
    await update.message.reply_text("Привет! Я бот для генерации мем-видео. Наберите /help для списка команд.")


async def cmd_help(update, context):
    await update.message.reply_text(HELP_TEXT)


async def cmd_generate(update, context):
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


def main():
    try:
        from telegram.ext import ApplicationBuilder, CommandHandler, CallbackQueryHandler
    except Exception as e:
        print(f"python-telegram-bot не установлен: {e}")
        return

    token = TELEGRAM_BOT_TOKEN or os.getenv("TELEGRAM_BOT_TOKEN")
    if not token:
        print("Не задан TELEGRAM_BOT_TOKEN в .env")
        return

    app = ApplicationBuilder().token(token).build()
    try:
        app.bot_data["dry_run"] = BOT_DRY_RUN_DEFAULT
    except Exception:
        pass

    app.add_handler(CommandHandler("start", cmd_start))
    app.add_handler(CommandHandler("help", cmd_help))
    app.add_handler(CommandHandler("generate", cmd_generate))
    app.add_handler(CommandHandler("deploy", cmd_deploy))
    app.add_handler(CommandHandler("history", cmd_history))
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

    print("Бот запущен. Нажмите Ctrl+C для остановки.")
    app.run_polling()


if __name__ == "__main__":
    main()
