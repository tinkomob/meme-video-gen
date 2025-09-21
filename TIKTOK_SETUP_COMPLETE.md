# TikTok Upload Setup Guide

## Интеграция TiktokAutoUploader прошла успешно! 🎉

### Статус
- ✅ **TiktokAutoUploader** клонирован и настроен
- ✅ **Node.js зависимости** установлены автоматически
- ✅ **Python зависимости** добавлены в requirements.txt
- ✅ **Функция загрузки** интегрирована в app/uploaders.py
- ✅ **Docker поддержка** настроена

### Как использовать

#### 1. Для загрузки в TikTok нужны куки авторизации:

```python
from app.uploaders import tiktok_upload

# С куками (файл или строка)
result = tiktok_upload(
    video_path='path/to/video.mp4',
    description='Мое крутое видео #мем',
    cookies='path/to/tiktok_cookies.txt'  # Файл с куками
)

# Без кук (покажет ошибку авторизации)
result = tiktok_upload(
    video_path='path/to/video.mp4', 
    description='Мое крутое видео #мем'
)
```

#### 2. Результат загрузки:
```python
{
    'success': True/False,
    'video_url': 'https://tiktok.com/...',  # при успехе
    'error': 'описание ошибки',            # при неудаче  
    'method': 'local_library'
}
```

### Получение кук TikTok

1. **Войдите в TikTok** через браузер
2. **Откройте DevTools** (F12)
3. **Перейдите на вкладку Application/Storage**
4. **Скопируйте все куки** с домена tiktok.com
5. **Сохраните в файл** в формате Netscape/Mozilla cookies

### Docker запуск

```bash
# Сборка образа
docker build -t meme-video-gen .

# Запуск с монтированием кук
docker run -v ./cookies:/app/cookies meme-video-gen
```

### Новые зависимости добавлены в requirements.txt:
- fake-useragent
- undetected-chromedriver  
- setuptools
- pytube
- requests-auth-aws-sigv4
- requests-html
- pyquery
- beautifulsoup4
- tqdm

### Заметки
- **Chrome больше не нужен!** TiktokAutoUploader использует requests
- **Оригинальная проблема решена:** "user data directory is already in use"
- **Функция имеет fallback:** сначала библиотека, потом CLI
- **Автоустановка зависимостей:** Node.js модули устанавливаются автоматически

### Возможные проблемы
1. **"No cookie with Tiktok session id found"** - нужно предоставить валидные куки
2. **"User not found on system"** - нужна авторизация через куки
3. **npm install failed** - проверьте установку Node.js

### Следующие шаги
1. Получите куки от TikTok аккаунта
2. Протестируйте реальную загрузку
3. Интегрируйте в бота с обработкой кук

Система готова к использованию! 🚀