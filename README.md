# Meme Video Generator — Telegram Bot

Генерация и публикация мем-видео через Telegram-бота. Проект включает:
- **Python версия** (bot.py) — генерирует видео из источников (Pinterest/Reddit/Twitter)
- **Go версия** (meme-bot) — отправляет готовые видео по расписанию N раз в день

## Быстрый старт

### Python версия (генерация видео)

```powershell
python -m venv .venv
. .venv\Scripts\Activate.ps1
pip install -r requirements.txt

# Создайте .env с переменными окружения
New-Item -ItemType File -Path .env -Force | Out-Null
Add-Content .env "TELEGRAM_BOT_TOKEN=ваш_токен_бота"
# Опционально для загрузок в соцсети
Add-Content .env "INSTAGRAM_USERNAME=..."
Add-Content .env "UPLOAD_POST_API_KEY=..."
Add-Content .env "YOUTUBE_API_KEY=..."

# Запуск бота
python bot.py
```

### Go версия (расписание отправок)

**НОВОЕ! Автоматическая отправка мемов N раз в день!**

```bash
# Создайте .env
echo "TELEGRAM_BOT_TOKEN=your_token
POSTS_CHAT_ID=-1001234567890
DAILY_GENERATIONS=3
S3_ENDPOINT=http://minio:9000
S3_REGION=us-east-1
S3_BUCKET=memes
S3_ACCESS_KEY=minioadmin
S3_SECRET_KEY=minioadmin" > .env

# Запустите
docker-compose up --build
```

**Начните с [QUICKSTART_SCHEDULE.md](QUICKSTART_SCHEDULE.md) для быстрого старта!**

### Pinterest Scraper CLI

Standalone инструмент для скачивания изображений с Pinterest с полным рендерингом JavaScript.

```powershell
# Компилирование
cd cmd\pinterest-scraper
go build -o pinterest-scraper.exe

# Использование (без параметров - автоматически)
.\pinterest-scraper.exe

# Указать папку для сохранения
.\pinterest-scraper.exe -output "C:\Downloads"
```

**Особенности:**
- ✓ Полный рендеринг JavaScript (Chromium headless)
- ✓ Обход lazy loading и динамической загрузки пинов
- ✓ Автоматический выбор лучшего качества изображения
- ✓ Fallback на Colly если Chrome недоступен

Требует: Chromium/Chrome браузер на системе

Подробнее: [cmd/pinterest-scraper/README.md](cmd/pinterest-scraper/README.md)

## Команды бота



### Python версия (bot.py)
- /start — приветствие
- /help — помощь
- /generate [pin_num] [audio_duration] — сгенерировать ролик
  - Примеры: `/generate`, `/generate 80`, `/generate 120 12`
- /deploy [socials=yt,instagram,x] [privacy=public|unlisted|private] — опубликовать последний ролик
  - Пример: `/deploy socials=yt,instagram privacy=unlisted`
- /history — последние публикации (локальная история)
- /scheduleinfo — расписание генераций на сегодня
- /runscheduled — выполнить ближайшую запланированную генерацию

### Go версия (meme-bot) — НОВОЕ!
- /meme — получить 1 случайный мем
- **/scheduleinfo** — показать расписание отправок на сегодня
- **/runscheduled** — отправить 3 мема в чат прямо сейчас
- /status — статус системы
- /errors — последние ошибки из logs
- /chatid — показать Chat ID
- /help — справка по командам

## 🎬 Загрузка собственного видео

**Новый функционал!** Теперь можно загружать свои видео для создания мемов:

1. **Отправьте видео** в чат с ботом
2. **Выберите способ добавления аудио:**
   - 🎲 **Случайный трек** — автоматический выбор из плейлистов
   - 📤 **Загрузить свой аудио** — MP3, WAV или голосовое сообщение
   - 🔍 **Поиск по плейлистам** — найти конкретный трек
3. **Бот обработает видео:**
   - Добавит выбранное аудио (случайный фрагмент 12 сек)
   - Применит эффекты и масштабирование
   - Создаст миниатюру
4. **Опубликуйте результат** или смените трек

Подробнее: [VIDEO_UPLOAD_FEATURE.md](VIDEO_UPLOAD_FEATURE.md)

## Переменные окружения для загрузки

### Instagram
```bash
INSTAGRAM_USERNAME=your_username
UPLOAD_POST_API_KEY=your_api_key  # API key from upload-post.com
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
- google_keywords.json — список ключевых слов для поиска изображений через Google Images (опционально, требуется SERPAPI_KEY)

Подробнее о Twitter интеграции: см. [TWITTER_INTEGRATION.md](TWITTER_INTEGRATION.md)

### Google Images (SerpAPI)
Для использования Google Images как источника изображений:
```bash
SERPAPI_KEY=your_serpapi_key  # Get from https://serpapi.com
```
Создайте `google_keywords.json` с ключевыми словами для поиска (по умолчанию используются "funny cat memes", "dank memes", etc.)

## Зависимости и требования
- Нужен ffmpeg в PATH для moviepy и yt-dlp постобработки
- Для Instagram: установите `pyotp` для поддержки 2FA (уже в requirements.txt)
- Для YouTube: client_secrets.json, token.pickle будет создан автоматически при OAuth
## Примечание
Старый FastAPI веб-интерфейс удалён из основного сценария. Используйте Telegram-бота.