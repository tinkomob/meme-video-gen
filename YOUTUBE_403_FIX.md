# Исправление ошибки YouTube 403 (Forbidden)

## Проблема
YouTube блокирует загрузку аудио с ошибкой `HTTP Error 403: Forbidden`. Это происходит из-за защиты YouTube от автоматизированных запросов.

## Решение: Использование cookies

### Шаг 1: Установка расширения для экспорта cookies

Установите одно из расширений:

**Для Chrome/Edge/Brave:**
1. Откройте [Chrome Web Store](https://chrome.google.com/webstore)
2. Найдите расширение **"Get cookies.txt LOCALLY"**
3. Нажмите "Добавить в Chrome"

**Для Firefox:**
1. Откройте [Firefox Add-ons](https://addons.mozilla.org)
2. Найдите расширение **"cookies.txt"**
3. Нажмите "Add to Firefox"

**Альтернатива:**
- **"EditThisCookie"** (Chrome/Edge)
- **"Cookie-Editor"** (Chrome/Firefox)

### Шаг 2: Получение cookies с YouTube

1. **Откройте YouTube** в браузере: https://www.youtube.com
2. **Войдите в аккаунт** (если еще не вошли)
3. Откройте любое видео или плейлист
4. **Нажмите на иконку расширения** (в правом верхнем углу браузера)
5. **Экспортируйте cookies** в формате Netscape:
   - Для "Get cookies.txt LOCALLY": нажмите кнопку "Export"
   - Для "cookies.txt": нажмите "Export" → выберите "Netscape format"
6. **Сохраните файл** как `youtube_cookies.txt`

### Шаг 3: Загрузка cookies в бот

#### Метод 1: Через Telegram бот (рекомендуется)
```
/uploadytcookies
```
Затем отправьте файл `youtube_cookies.txt` как документ.

#### Метод 2: Через Docker (прямая копия)
```bash
# Если бот запущен в Docker
docker cp youtube_cookies.txt <container_id>:/app/youtube_cookies.txt
docker restart <container_id>
```

#### Метод 3: Через SSH/FTP
Загрузите файл `youtube_cookies.txt` в корень проекта (туда же, где `bot.py`).

### Шаг 4: Проверка

После загрузки cookies выполните:
```
/checkfiles
```

Должно показать:
```
✅ YouTube youtube_cookies.txt: найден (путь: youtube_cookies.txt)
```

## Важные замечания

### ⚠️ Безопасность cookies
- **Не делитесь cookies файлом** - он содержит данные вашей сессии
- **Обновляйте cookies** если они истекли (обычно через 1-3 месяца)
- **Используйте отдельный аккаунт** для бота (не основной)

### 🔄 Когда обновлять cookies
Обновите cookies если:
- Снова появляется ошибка 403
- Прошло больше 2 месяцев с момента создания
- Вы сменили пароль на YouTube аккаунте
- Вы вышли из аккаунта в браузере

### 📋 Формат файла cookies
Файл должен быть в формате **Netscape HTTP Cookie File**:
```
# Netscape HTTP Cookie File
.youtube.com	TRUE	/	TRUE	1234567890	VISITOR_INFO1_LIVE	abcdef123456
.youtube.com	TRUE	/	FALSE	1234567890	PREF	f1=50000000
...
```

### ✅ Проверка корректности cookies

**Правильный формат:**
- Начинается с `# Netscape HTTP Cookie File`
- Содержит строки с доменом `.youtube.com`
- Размер файла: обычно 5-20 КБ

**Неправильный формат:**
- JSON формат `{"cookies": [...]}`
- Пустой файл
- Только заголовок без cookies

## Альтернативные решения

### Вариант 1: Использование browser cookies напрямую
Если не хотите экспортировать cookies в файл, можно настроить переменные окружения:

**.env файл:**
```bash
YT_COOKIES_FROM_BROWSER=chrome
YT_COOKIES_PROFILE=Default
```

Или:
```bash
YT_COOKIES_FROM_BROWSER=firefox
YT_COOKIES_PROFILE=default-release
```

### Вариант 2: User Agent
Добавьте в `.env`:
```bash
YT_USER_AGENT="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
```

### Вариант 3: Impersonation (экспериментально)
```bash
YT_IMPERSONATE=chrome120
```

## Диагностика проблем

### Ошибка все еще возникает после загрузки cookies

1. **Проверьте формат файла:**
   ```bash
   head -5 youtube_cookies.txt
   ```
   Должно начинаться с `# Netscape HTTP Cookie File`

2. **Проверьте размер файла:**
   ```bash
   ls -lh youtube_cookies.txt
   ```
   Должен быть > 1 KB

3. **Проверьте, что cookies актуальны:**
   - Зайдите на YouTube в браузере
   - Если нужно войти заново - cookies истекли
   - Экспортируйте cookies заново

4. **Попробуйте другой браузер:**
   - Chrome cookies иногда работают лучше Firefox и наоборот
   - Попробуйте режим Incognito для чистого профиля

5. **Проверьте логи:**
   ```bash
   grep "cookies" errors.log
   grep "403" errors.log
   ```

### Cookies файл не обнаруживается

1. **Проверьте имя файла:**
   - Должен быть точно `youtube_cookies.txt` (без пробелов)
   - Не `youtube_cookies.txt.txt` (скрытое расширение Windows)

2. **Проверьте расположение:**
   - Должен быть в корне проекта (рядом с `bot.py`)
   - Не в подпапке

3. **Проверьте права доступа:**
   ```bash
   chmod 644 youtube_cookies.txt
   ```

## Пример успешной работы

После правильной настройки cookies вы увидите в логах:
```
Using YouTube cookies file: youtube_cookies.txt (8542 bytes)
Selected video: https://www.youtube.com/watch?v=xxxxx
Downloaded audio path: audio/xxxxx.mp3
✅ Аудио успешно добавлено: Artist - Song Name
```

Вместо:
```
ERROR: unable to download video data: HTTP Error 403: Forbidden
❌ YouTube блокирует загрузку (403)
```

## Улучшения в коде

Добавлены улучшения для обхода блокировки:

1. **Автоматическое определение cookies:**
   - Проверка `youtube_cookies.txt` в корне проекта
   - Поддержка переменных `YT_COOKIES_FILE` и `YTDLP_COOKIES_FILE`

2. **Умная обработка 403 ошибок:**
   - Немедленное предупреждение если нет cookies
   - Понятное сообщение о решении проблемы
   - Прерывание retry если точно нужны cookies

3. **Улучшенная совместимость с YouTube:**
   - Использование Android и Web player clients
   - Настройка User-Agent по умолчанию
   - Поддержка impersonation для обхода детекции

4. **Расширенное логирование:**
   - Показывает размер cookies файла
   - Логирует использование cookies
   - Предупреждает если cookies не найдены

## Частые вопросы

**Q: Нужно ли иметь YouTube Premium?**  
A: Нет, обычный бесплатный аккаунт подходит.

**Q: Сколько действуют cookies?**  
A: Обычно 1-3 месяца, зависит от настроек YouTube.

**Q: Можно ли использовать cookies с нескольких аккаунтов?**  
A: Нет, используйте cookies только одного аккаунта.

**Q: Безопасно ли это?**  
A: Да, cookies используются только для доступа к YouTube API. Не храните cookies основного аккаунта.

**Q: Что делать если cookies часто истекают?**  
A: Используйте "Remember me" при входе в YouTube, или настройте browser cookies extraction.

## Полезные ссылки

- [Get cookies.txt LOCALLY (Chrome)](https://chrome.google.com/webstore/detail/get-cookiestxt-locally/cclelndahbckbenkjhflpdbgdldlbecc)
- [cookies.txt (Firefox)](https://addons.mozilla.org/en-US/firefox/addon/cookies-txt/)
- [yt-dlp документация](https://github.com/yt-dlp/yt-dlp#authentication-options)
- [YouTube API Status](https://www.google.com/appsstatus/dashboard/)
