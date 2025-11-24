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
- /deploy [socials=yt,instagram,x] [privacy=public|unlisted|private] — опубликовать последний ролик
  - Пример: `/deploy socials=yt,instagram privacy=unlisted`
- /history — последние публикации (локальная история)

## Переменные окружения для загрузки

### Instagram
```bash
INSTAGRAM_USERNAME=your_username
INSTAGRAM_PASSWORD=your_password
INSTAGRAM_TOTP_SECRET=your_totp_secret  # Опционально для 2FA
INSTAGRAM_PROXY=http://proxy:port       # Опционально
```

### YouTube
Требуется настройка OAuth через `client_secrets.json`.

### X (Twitter)
```bash
# Option 1: Bearer Token (RECOMMENDED - simpler)
X_BEARER_TOKEN=your_bearer_token

# Option 2: OAuth 1.0a (requires elevated access)
X_CONSUMER_KEY=your_consumer_key
X_CONSUMER_SECRET=your_consumer_secret
X_ACCESS_TOKEN=your_access_token
X_ACCESS_TOKEN_SECRET=your_access_token_secret
```

Get Bearer Token: [Developer Portal](https://developer.twitter.com/en/portal/dashboard) → Your App → Keys and tokens → Generate Bearer Token

## Подготовка данных
- pinterest_urls.json — список Pinterest URL (board/search) для загрузки картинок/видео
- music_playlists.json — список ссылок на YouTube плейлисты для фоновой музыки
- reddit_sources.json — список сабреддитов или ссылок на сабреддиты (например: "wtfstockphotos", "r/memes", "https://www.reddit.com/r/ProgrammerHumor/")
- twitter_urls.json — список Twitter/X аккаунтов для загрузки изображений (например: "https://x.com/imagesooc", "@nocontextimg", "weirddalle")

Подробнее о Twitter интеграции: см. [TWITTER_INTEGRATION.md](TWITTER_INTEGRATION.md)

## Зависимости и требования
- Нужен ffmpeg в PATH для moviepy и yt-dlp постобработки
- Для Instagram: установите `pyotp` для поддержки 2FA (уже в requirements.txt)
- Для YouTube: client_secrets.json, token.pickle будет создан автоматически при OAuth
## Примечание
Старый FastAPI веб-интерфейс удалён из основного сценария. Используйте Telegram-бота.