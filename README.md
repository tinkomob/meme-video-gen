# Meme Video Generator — Telegram Bot

Генерация и публикация мем-видео через Telegram-бота (python-telegram-bot).

## Быстрый старт (Windows PowerShell)

```powershell
python -m venv .venv
. .venv\Scripts\Activate.ps1
pip install -r requirements.txt

# Создайте .env с переменными окружения
New-Item -ItemType File -Path .env -Force | Out-Null
Add-Content .env "TELEGRAM_BOT_TOKEN=ваш_токен_бота"
# Опционально для загрузок в соцсети
Add-Content .env "INSTAGRAM_USERNAME=..."
Add-Content .env "INSTAGRAM_PASSWORD=..."
Add-Content .env "YOUTUBE_API_KEY=..."

# Запуск бота
python bot.py
```

## Команды бота
- /start — приветствие
- /help — помощь
- /generate [pin_num] [audio_duration] — сгенерировать ролик
  - Примеры: `/generate`, `/generate 80`, `/generate 120 12`
- /deploy [socials=yt,instagram,tiktok,x] [privacy=public|unlisted|private] — опубликовать последний ролик
  - Пример: `/deploy socials=yt,instagram privacy=unlisted`
- /history — последние публикации (локальная история)

## Подготовка данных
- pinterest_urls.json — список Pinterest URL (board/search) для загрузки картинок/видео
- music_playlists.json — список ссылок на YouTube плейлисты для фоновой музыки

## Зависимости и требования
- Нужен ffmpeg в PATH для moviepy и yt-dlp постобработки
- Для Instagram: INSTAGRAM_USERNAME/INSTAGRAM_PASSWORD
- Для YouTube: client_secrets.json, token.pickle будет создан автоматически при OAuth
- Для TikTok: cookies.txt, опции в app/config.py

## Примечание
Старый FastAPI веб-интерфейс удалён из основного сценария. Используйте Telegram-бота.
