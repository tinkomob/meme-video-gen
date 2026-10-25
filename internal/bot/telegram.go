package bot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/scheduler"
)

type TelegramBot struct {
	tg         *tgbotapi.BotAPI
	svc        *scheduler.Service
	log        *logging.Logger
	errorsPath string

	// cancelFunc stops the entire application (used by memory watcher for emergency shutdown)
	cancelFunc context.CancelFunc

	// Track search state (chatID -> is searching)
	trackSearchState     map[int64]bool
	trackSearchMux       sync.Mutex
	trackSearchTimestamp map[int64]time.Time // TTL entry: when search mode was set

	trackSearchMode    map[int64]string
	trackSearchModeMux sync.Mutex
	snippetRunning     atomic.Bool
	snippetActionMux   sync.Mutex
	snippetActions     map[string]bool
}

func NewTelegramBot(svc *scheduler.Service, log *logging.Logger, errorsPath string, cancel context.CancelFunc) (*TelegramBot, error) {
	tok := os.Getenv("TELEGRAM_BOT_TOKEN")
	if tok == "" {
		return nil, errors.New("TELEGRAM_BOT_TOKEN is empty")
	}
	httpClient := &http.Client{Timeout: 60 * time.Second}
	api, err := tgbotapi.NewBotAPIWithClient(tok, tgbotapi.APIEndpoint, httpClient)
	if err != nil {
		return nil, err
	}
	api.Debug = false
	return &TelegramBot{
		cancelFunc:           cancel,
		tg:                   api,
		svc:                  svc,
		log:                  log,
		errorsPath:           errorsPath,
		trackSearchState:     make(map[int64]bool),
		trackSearchTimestamp: make(map[int64]time.Time),
		trackSearchMode:      make(map[int64]string),
	}, nil
}

func (b *TelegramBot) Run(ctx context.Context) error {
	b.updateCommandMenu()
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := b.tg.GetUpdatesChan(u)
	b.log.Infof("telegram bot started as @%s", b.tg.Self.UserName)

	// Start memory leak watcher.
	go b.runMemoryWatcher(ctx)

	// Send startup notification to the bot owner's personal chat.
	if cfg := b.svc.GetConfig(); cfg.OwnerChatID != 0 {
		b.replyText(cfg.OwnerChatID, "✅ Бот запущен и готов к работе.")
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case upd := <-updates:
			if upd.Message != nil && upd.Message.IsCommand() {
				b.handleCommand(ctx, upd.Message)
			} else if upd.Message != nil && upd.Message.Text != "" {
				chatID := upd.Message.Chat.ID

				// Check if user is in track search mode
				b.trackSearchMux.Lock()
				isSearching := b.trackSearchState[chatID]
				if isSearching {
					// Expire search intent after 5 minutes of inactivity
					if ts, ok := b.trackSearchTimestamp[chatID]; !ok || time.Since(ts) > 5*time.Minute {
						isSearching = false
					}
					delete(b.trackSearchState, chatID)
					delete(b.trackSearchTimestamp, chatID)
				}
				b.trackSearchMux.Unlock()

				if isSearching {
					// Get the search mode
					b.trackSearchModeMux.Lock()
					mode := b.trackSearchMode[chatID]
					delete(b.trackSearchMode, chatID) // Clear the mode
					b.trackSearchModeMux.Unlock()

					// Handle search based on mode
					if mode == "song" {
						b.handleSongSearch(ctx, chatID, upd.Message.Text)
					}
				}
			} else if upd.CallbackQuery != nil {
				b.handleCallback(ctx, upd.CallbackQuery)
			}
		}
	}
}

func (b *TelegramBot) handleCommand(ctx context.Context, msg *tgbotapi.Message) {
	cmd := msg.Command()
	chatID := msg.Chat.ID

	switch cmd {
	case "start":
		b.replyText(chatID, "Привет! Я бот для музыкальных сниппетов. Наберите /help для списка команд.")
	case "help":
		b.cmdHelp(chatID)
	case "chatid":
		b.cmdChatID(chatID)
	case "song":
		b.cmdSong(ctx, chatID, msg.CommandArguments())
	case "snippet":
		if msg.From != nil {
			b.cmdSnippet(ctx, chatID, msg.From.ID, msg.CommandArguments())
		}
	default:
		b.replyText(chatID, "Неизвестная команда. Используйте /help")
	}
}

func (b *TelegramBot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	if cb == nil || cb.Message == nil || cb.From == nil {
		return
	}
	if strings.HasPrefix(cb.Data, "snip:") {
		b.handleSnippetCallback(ctx, cb)
		return
	}
	b.tg.Request(tgbotapi.NewCallback(cb.ID, ""))

	data := cb.Data
	chatID := cb.Message.Chat.ID

	// Handle single-action callbacks first (no parameters needed)
	if data == "songsearch" {
		// Set flag to wait for text input
		b.trackSearchMux.Lock()
		b.trackSearchState[chatID] = true
		b.trackSearchTimestamp[chatID] = time.Now()
		b.trackSearchMux.Unlock()

		b.trackSearchModeMux.Lock()
		b.trackSearchMode[chatID] = "song"
		b.trackSearchModeMux.Unlock()

		b.replyText(chatID, "🔍 Введите название трека или артиста для поиска:\n(например: Dua Lipa или The Weeknd)")
		return
	}

	if data == "songrand" {
		b.handleSongDownloadRandom(ctx, chatID)
		return
	}

	// Parse callback data
	parts := splitCallback(data)
	if len(parts) < 2 {
		b.replyText(chatID, "❌ Некорректные данные кнопки")
		return
	}

	action := parts[0]
	itemID := parts[1]

	switch action {
	case "songlist":
		offset := 0
		fmt.Sscanf(itemID, "%d", &offset)
		b.handleSongList(ctx, chatID, offset)
	case "songdl":
		b.handleSongDownload(ctx, chatID, itemID)
	default:
		b.replyText(chatID, "❌ Неизвестное действие")
	}
}

func splitCallback(data string) []string {
	var result []string
	current := ""
	for _, ch := range data {
		if ch == ':' {
			result = append(result, current)
			current = ""
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func (b *TelegramBot) updateCommandMenu() {
	commands := tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "start", Description: "Запустить бота"},
		tgbotapi.BotCommand{Command: "help", Description: "Список команд"},
		tgbotapi.BotCommand{Command: "snippet", Description: "Создать музыкальный сниппет"},
		tgbotapi.BotCommand{Command: "song", Description: "Найти и скачать песню"},
		tgbotapi.BotCommand{Command: "chatid", Description: "Показать ID чата"},
	)
	if _, err := b.tg.Request(commands); err != nil {
		b.log.Warnf("could not update Telegram command menu: %s", strings.ReplaceAll(err.Error(), b.tg.Token, "[redacted]"))
	}
}

func (b *TelegramBot) cmdHelp(chatID int64) {
	help := `Команды:
/start — приветствие
/help — помощь
/snippet [стиль] [вариант] — создать 20-секундный сниппет в этом чате
              /snippet — случайный стиль и вариант
              /snippet galaxy 2 — выбрать оформление
              Под видео: площадки публикации, новый визуал, смена/выбор песни и удаление черновика.
              Стили: silver, violet, amber, aurora, prism, galaxy
              Варианты: 0, 1, 2. Плановые публикации: 2 случайных времени в день (Томск), интервалы 10–14 часов, без уведомлений.
/songs — загрузить новые треки в S3 (songs/)
/song [query] — скачать трек в формате MP3 или MP4A
              /song — выбрать из списка, случайный или поиск
              /song Dua Lipa — найти треки Dua Lipa и скачать
/status — статус генерации и использование памяти
/errors — скачать файл errors.log с последними ошибками
/chatid — показать текущий chat ID
/checkfiles — проверить наличие и размер файлов (token.pickle, client_secrets.json)
/uploadtoken — загрузить token.pickle как документ
/uploadclient — загрузить client_secrets.json как документ
/syncfiles — загрузить все файлы в S3
/downloadfiles — загрузить все файлы из S3 локально

Этот бот создаёт и публикует 20-секундные музыкальные сниппеты.`
	b.replyText(chatID, help)
}

func (b *TelegramBot) cmdErrors(chatID int64) {
	f, err := os.Open(b.errorsPath)
	if err != nil {
		b.log.Errorf("open errors.log: %v", err)
		b.replyText(chatID, "❌ Не удалось открыть errors.log")
		return
	}
	defer f.Close()

	// Check if file is empty
	info, err := f.Stat()
	if err != nil {
		b.log.Errorf("stat errors.log: %v", err)
		b.replyText(chatID, "❌ Ошибка чтения errors.log")
		return
	}

	if info.Size() == 0 {
		b.replyText(chatID, "📋 errors.log пуст")
		return
	}

	msg := tgbotapi.NewDocument(chatID, tgbotapi.FileReader{Name: "errors.log", Reader: f})
	msg.Caption = fmt.Sprintf("📋 errors.log (%d байт)", info.Size())

	_, err = b.tg.Send(msg)
	if err != nil {
		b.log.Errorf("send errors.log: %v", err)
		b.replyText(chatID, "❌ Ошибка отправки файла")
		return
	}
}

func (b *TelegramBot) cmdChatID(chatID int64) {
	b.replyText(chatID, fmt.Sprintf("Ваш Chat ID: %d", chatID))
}

func (b *TelegramBot) handleSongList(ctx context.Context, chatID int64, offset int) {
	b.log.Infof("handleSongList: START - offset=%d", offset)

	// Get songs from storage
	allSongs, err := b.svc.GetAllSongs(ctx)
	if err != nil || len(allSongs) == 0 {
		b.replyText(chatID, "❌ Ошибка: нет доступных песен")
		return
	}

	// Limit to last 8 songs for pagination
	const itemsPerPage = 8
	if offset < 0 {
		offset = 0
	}
	if offset >= len(allSongs) {
		offset = len(allSongs) - 1
	}

	endIdx := offset + itemsPerPage
	if endIdx > len(allSongs) {
		endIdx = len(allSongs)
	}

	songs := allSongs[offset:endIdx]
	b.log.Infof("handleSongList: showing %d songs (offset=%d, total=%d)", len(songs), offset, len(allSongs))

	// Create buttons for songs (max 8)
	rows := make([][]tgbotapi.InlineKeyboardButton, 0)
	for _, song := range songs {
		// Truncate title to fit button
		title := song.Title
		if len(title) > 30 {
			title = title[:27] + "..."
		}

		btn := tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("%s - %s", song.Author, title),
			fmt.Sprintf("songdl:%s", song.ID),
		)
		rows = append(rows, []tgbotapi.InlineKeyboardButton{btn})
	}

	// Add pagination buttons
	navRow := make([]tgbotapi.InlineKeyboardButton, 0)
	if offset > 0 {
		navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData(
			"⬅️ Предыдущие",
			fmt.Sprintf("songlist:%d", offset-itemsPerPage),
		))
	}
	if endIdx < len(allSongs) {
		navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData(
			"➡️ Следующие",
			fmt.Sprintf("songlist:%d", endIdx),
		))
	}
	if len(navRow) > 0 {
		rows = append(rows, navRow)
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("🎵 Выберите трек (%d-%d из %d):", offset+1, endIdx, len(allSongs)))
	msg.ReplyMarkup = keyboard
	b.tg.Send(msg)
}

func (b *TelegramBot) handleSongDownloadRandom(ctx context.Context, chatID int64) {
	b.log.Infof("handleSongDownloadRandom: START")

	song, err := b.svc.GetRandomSong(ctx)
	if err != nil {
		b.log.Errorf("handleSongDownloadRandom: get random song failed: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка: %v", err))
		return
	}

	b.handleSongDownload(ctx, chatID, song.ID)
}

func (b *TelegramBot) handleSongDownload(ctx context.Context, chatID int64, songID string) {
	b.log.Infof("handleSongDownload: START - songID=%s", songID)

	// Get song info
	song, err := b.svc.GetSongByID(ctx, songID)
	if err != nil {
		b.log.Errorf("handleSongDownload: get song failed: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка: песня не найдена - %v", err))
		return
	}

	// Send status message
	statusMsg := b.replyText(chatID, "⏳ Скачиваю трек...")

	go func() {
		b.log.Infof("handleSongDownload: downloading song: %s (%s)", song.Title, song.Author)

		// Download song to temp file
		songPath, err := b.svc.Impl().DownloadSongToTemp(ctx, song)
		if err != nil {
			b.log.Errorf("handleSongDownload: download song failed: %v", err)
			b.editMessageHTML(chatID, statusMsg, fmt.Sprintf("❌ Ошибка загрузки песни: %v", err))
			return
		}
		defer os.Remove(songPath)

		// Open file
		f, err := os.Open(songPath)
		if err != nil {
			b.log.Errorf("handleSongDownload: open song file: %v", err)
			b.editMessageHTML(chatID, statusMsg, "❌ Ошибка открытия файла")
			return
		}
		defer f.Close()

		// Construct S3 URL for download link
		s3URL := ""
		cfg := b.svc.GetConfig()
		if cfg.S3Endpoint != "" && cfg.S3Bucket != "" && song.AudioKey != "" {
			// Format: https://bucket.endpoint/key or https://endpoint/bucket/key
			if strings.Contains(cfg.S3Endpoint, "amazonaws.com") {
				// AWS S3 format: https://bucket.s3.region.amazonaws.com/key
				s3URL = fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(cfg.S3Endpoint, "/"), cfg.S3Bucket, song.AudioKey)
			} else {
				// MinIO format: https://endpoint/bucket/key
				s3URL = fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(cfg.S3Endpoint, "/"), cfg.S3Bucket, song.AudioKey)
			}
		}

		// Send audio file
		msg := tgbotapi.NewAudio(chatID, tgbotapi.FileReader{Name: "song.m4a", Reader: f})
		msg.Title = song.Title
		msg.Performer = song.Author

		caption := fmt.Sprintf("🎵 %s\n👤 %s", song.Title, song.Author)
		if s3URL != "" {
			caption += fmt.Sprintf("\n\n📥 <a href=\"%s\">Скачать из S3</a>", s3URL)
		}
		msg.Caption = caption
		msg.ParseMode = "HTML"

		if _, err := b.tg.Send(msg); err != nil {
			b.log.Errorf("handleSongDownload: send audio: %v", err)
			b.editMessageHTML(chatID, statusMsg, "❌ Ошибка отправки аудиофайла")
			return
		}

		// Delete status message
		b.tg.Send(tgbotapi.NewDeleteMessage(chatID, statusMsg))

		b.log.Infof("handleSongDownload: song sent successfully - %s (%s)", song.Title, song.Author)
	}()
}

func (b *TelegramBot) handleSongSearch(ctx context.Context, chatID int64, query string) {
	b.log.Infof("handleSongSearch: START - query=%s", query)

	// Search for songs matching the query
	songs, err := b.svc.SearchSongs(ctx, query)
	if err != nil {
		b.log.Errorf("handleSongSearch: search failed: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка поиска: %v", err))
		return
	}

	if len(songs) == 0 {
		b.replyText(chatID, fmt.Sprintf("❌ Треков не найдено по запросу: \"%s\"", query))
		return
	}

	b.log.Infof("handleSongSearch: found %d songs matching \"%s\"", len(songs), query)

	// Create buttons for found songs (max 10)
	maxResults := 10
	displayCount := len(songs)
	if displayCount > maxResults {
		displayCount = maxResults
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0)
	for i := 0; i < displayCount; i++ {
		song := songs[i]

		// Truncate title to fit button
		title := song.Title
		if len(title) > 30 {
			title = title[:27] + "..."
		}

		btn := tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("%s - %s", song.Author, title),
			fmt.Sprintf("songdl:%s", song.ID),
		)
		rows = append(rows, []tgbotapi.InlineKeyboardButton{btn})
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	resultText := fmt.Sprintf("🎵 Найдено треков: %d\nПоказано: %d", len(songs), displayCount)
	if displayCount < len(songs) {
		resultText += "\n\n❗Показаны первые 10 результатов"
	}
	msg := tgbotapi.NewMessage(chatID, resultText)
	msg.ReplyMarkup = keyboard
	b.tg.Send(msg)
}

func (b *TelegramBot) cmdSong(ctx context.Context, chatID int64, args string) {
	b.log.Infof("cmdSong: START - args=%s", args)

	args = strings.TrimSpace(args)

	// If user provided search query
	if args != "" {
		b.handleSongSearch(ctx, chatID, args)
		return
	}

	// Get songs from storage
	songs, err := b.svc.GetAllSongs(ctx)
	if err != nil || len(songs) == 0 {
		b.replyText(chatID, "❌ Ошибка: нет доступных песен")
		return
	}

	b.log.Infof("cmdSong: got %d songs", len(songs))

	// Create inline keyboard with options
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🎲 Случайный трек", "songrand"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Выбрать трек из списка", "songlist:0"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔍 Поиск трека", "songsearch"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, "🎵 Скачать трек\n\nВыберите трек, возьмите случайный или найдите по названию:")
	msg.ReplyMarkup = keyboard
	b.tg.Send(msg)
}

func (b *TelegramBot) replyText(chatID int64, text string) int {
	m := tgbotapi.NewMessage(chatID, text)
	sent, _ := b.tg.Send(m)
	return sent.MessageID
}

func (b *TelegramBot) replyHTML(chatID int64, text string) int {
	m := tgbotapi.NewMessage(chatID, text)
	m.ParseMode = tgbotapi.ModeHTML
	sent, _ := b.tg.Send(m)
	return sent.MessageID
}

func (b *TelegramBot) replyHTMLSilent(chatID int64, text string, silent bool) int {
	m := tgbotapi.NewMessage(chatID, text)
	m.ParseMode = tgbotapi.ModeHTML
	m.DisableNotification = silent
	sent, _ := b.tg.Send(m)
	return sent.MessageID
}
func (b *TelegramBot) editMessageHTML(chatID int64, messageID int, text string) error {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = tgbotapi.ModeHTML
	_, err := b.tg.Send(edit)
	return err
}

func (b *TelegramBot) editMessage(chatID int64, messageID int, text string) error {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	_, err := b.tg.Send(edit)
	return err
}
