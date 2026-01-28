# Meme Video Generator (Go)

Полностью переписанная версия на Go. Автоматически генерирует мем-видео и хранит их в S3. Telegram-бот выдает случайные мемы по команде `/meme`.

## Архитектура

- **audio** — индексирует треки из YouTube плейлистов (kkdai/youtube), сохраняет в S3
- **sources** — скрапит картинки/видео из Pinterest/Reddit/Twitter/Google Images (gocolly/colly + SerpAPI), ротирует по давности (<24ч) и лимиту (50)
- **video** — генерирует мем-видео (mowshon/moviego), дедуп по SHA256, хранит до 10 штук в S3
- **ai** — генерирует креативные названия через Google Gemini AI
- **s3** — универсальный клиент для работы с S3-совместимым хранилищем + JSON-индексы
- **bot** — Telegram-бот с командами `/meme`, `/errors` (последние 50 строк)
- **scheduler** — cron-задачи (раз в час обновляет аудио/источники/мемы)

## Быстрый старт

### Зависимости

```pwsh
# Go 1.22+
go version

# FFmpeg для moviego
# Скачайте с https://ffmpeg.org/download.html и добавьте в PATH
ffmpeg -version
```

### Конфигурация

Скопируйте `.env.example` в `.env` и заполните:

```env
TELEGRAM_BOT_TOKEN=...
S3_ENDPOINT=https://s3.twcstorage.ru
S3_ACCESS_KEY_ID=...
S3_SECRET_ACCESS_KEY=...
S3_REGION=ru-1
S3_BUCKET=...
GEMINI_API_KEY=...  # опционально, для AI-названий
SERPAPI_KEY=...     # опционально, для Google Images
```

Убедитесь, что JSON-конфиги на месте:
- `music_playlists.json` — массив YouTube playlist URLs
- `pinterest_urls.json` — массив Pinterest board URLs
- `reddit_sources.json` — массив Reddit URLs (r/subreddit)
- `twitter_urls.json` — массив Twitter handles (пока заглушка)
- `google_keywords.json` — массив ключевых слов для Google Images (опционально)

### Сборка и запуск

```pwsh
# Установить зависимости
go mod tidy

# Собрать
go build -o meme-bot.exe ./cmd/meme-bot

# Запустить
.\meme-bot.exe
```

Или через Makefile (если установлен make):

```pwsh
make build
make run
```

### Docker (опционально)

```pwsh
docker build -t meme-bot .
docker run --env-file .env -v ${PWD}/errors.log:/app/errors.log meme-bot
```

## Команды бота

- `/start`, `/help` — помощь
- `/meme` — получить случайный мем из готовых
- `/errors` — последние 50 строк из `errors.log`

## Логика работы

1. **При старте** — запускается scheduler с cron-расписанием (каждый час)
2. **Через 5 секунд** — выполняется initial run:
   - Загружаются треки из плейлистов → `songs.json` в S3
   - Скрапятся картинки → `sources.json` в S3 (макс 50, старше 24ч удаляются)
   - Генерируются мемы (до 10) → `memes.json` в S3
3. **Каждый час** — повторяются шаги выше (пополнение индексов)
4. **При команде `/meme`** — бот выбирает случайный мем из `memes.json`, скачивает из S3 и отправляет пользователю

## Структура S3

```
songs/           - аудиофайлы (.m4a)
sources/         - картинки/видео (.jpg, .png)
memes/           - готовые видео (.mp4) + миниатюры (_thumb.jpg)
tokens/          - OAuth токены (если потребуются в будущем)
songs.json       - индекс треков
sources.json     - индекс источников
memes.json       - индекс мемов
```

## Разработка

### Добавить новый источник

1. Реализуйте функцию `scrapeXYZ()` в `internal/sources/scraper.go`
2. Добавьте вызов в `EnsureSources()`
3. Создайте JSON-конфиг (например, `xyz_urls.json`)

### Добавить новую команду бота

1. Добавьте case в `bot.Run()` (`internal/bot/telegram.go`)
2. Реализуйте handler-функцию

### Кастомизировать генерацию видео

Измените логику в `video.generateOne()` — там вызывается moviego для применения эффектов.

## Известные ограничения

- **Twitter scraping** — требует API ключи или авторизацию, сейчас заглушка
- **moviego** — зависит от ffmpeg; убедитесь, что ffmpeg доступен в PATH
- **Thumbnail** — пока placeholder (нужно добавить экстракт кадра через ffmpeg)
- **AI titles** — опциональны; если ключ не указан, используется fallback "Мем под трек: ..."

## Лицензия

MIT (см. LICENSE)
