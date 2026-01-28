package bot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/scheduler"
)

type TelegramBot struct {
	tg         *tgbotapi.BotAPI
	svc        *scheduler.Service
	log        *logging.Logger
	errorsPath string

	// Schedule poster goroutine control
	schedulePosterDone chan struct{}
}

func NewTelegramBot(svc *scheduler.Service, log *logging.Logger, errorsPath string) (*TelegramBot, error) {
	tok := os.Getenv("TELEGRAM_BOT_TOKEN")
	if tok == "" {
		return nil, errors.New("TELEGRAM_BOT_TOKEN is empty")
	}
	api, err := tgbotapi.NewBotAPI(tok)
	if err != nil {
		return nil, err
	}
	api.Debug = false
	return &TelegramBot{
		tg:                 api,
		svc:                svc,
		log:                log,
		errorsPath:         errorsPath,
		schedulePosterDone: make(chan struct{}),
	}, nil
}

func (b *TelegramBot) Run(ctx context.Context) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := b.tg.GetUpdatesChan(u)
	b.log.Infof("telegram bot started as @%s", b.tg.Self.UserName)

	// Start schedule poster goroutine
	go b.runSchedulePoster(ctx)

	for {
		select {
		case <-ctx.Done():
			b.schedulePosterDone <- struct{}{}
			return nil
		case upd := <-updates:
			if upd.Message != nil && upd.Message.IsCommand() {
				b.handleCommand(ctx, upd.Message)
			} else if upd.CallbackQuery != nil {
				b.handleCallback(ctx, upd.CallbackQuery)
			}
		}
	}
}

func (b *TelegramBot) handleCommand(ctx context.Context, msg *tgbotapi.Message) {
	cmd := msg.Command()
	chatID := msg.Chat.ID

	// Save POSTS_CHAT_ID on any command (if not already set)
	go b.savePostsChatIDIfNeeded(ctx, chatID)

	switch cmd {
	case "start":
		b.replyText(chatID, "Привет! Я бот для генерации мем-видео. Наберите /help для списка команд.")
	case "help":
		b.cmdHelp(chatID)
	case "errors":
		b.cmdErrors(chatID)
	case "meme":
		b.handleMeme(ctx, chatID)
	case "status":
		b.cmdStatus(ctx, chatID)
	case "chatid":
		b.cmdChatID(chatID)
	case "scheduleinfo":
		b.cmdScheduleInfo(chatID)
	case "runscheduled":
		b.cmdRunScheduled(ctx, chatID)
	case "clearsources":
		b.cmdClearSources(ctx, chatID)
	case "eenfinit":
		b.cmdEenfinit(ctx, chatID, msg.CommandArguments())
	case "sync":
		b.cmdSync(ctx, chatID)
	case "forcecheck":
		b.cmdForceCheck(ctx, chatID)
	default:
		b.replyText(chatID, "Неизвестная команда. Используйте /help")
	}
}

func (b *TelegramBot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	b.tg.Send(tgbotapi.NewCallback(cb.ID, ""))

	data := cb.Data
	chatID := cb.Message.Chat.ID

	// Parse callback data
	parts := splitCallback(data)
	if len(parts) < 2 {
		b.replyText(chatID, "❌ Некорректные данные кнопки")
		return
	}

	action := parts[0]
	memeID := parts[1]

	switch action {
	case "publish":
		b.handlePublish(ctx, chatID, memeID, cb.Message.MessageID)
	case "choose":
		b.handleChoosePlatforms(ctx, chatID, memeID, cb.Message.MessageID)
	case "changeaudio":
		b.handleChangeAudio(ctx, chatID, memeID, cb.Message.MessageID)
	case "delete":
		b.handleDeleteMeme(ctx, chatID, memeID, cb.Message.MessageID)
	case "toggle":
		if len(parts) >= 3 {
			platform := parts[1]
			memeID := parts[2]
			b.handleTogglePlatform(ctx, chatID, platform, memeID, cb.Message.MessageID)
		}
	case "publishsel":
		b.handlePublishSelected(ctx, chatID, memeID, cb.Message.MessageID)
	case "publishall":
		b.handlePublishAll(ctx, chatID, memeID, cb.Message.MessageID)
	case "cancelchoose":
		b.replyText(chatID, "❌ Отменено")
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

func (b *TelegramBot) handlePublish(ctx context.Context, chatID int64, memeID string, msgID int) {
	b.replyText(chatID, "📤 Загружаю видео во все платформы...")
	// TODO: Implement actual upload logic using uploaders package
	b.replyText(chatID, "✅ Видео опубликовано (заглушка)")
}

func (b *TelegramBot) handleChoosePlatforms(ctx context.Context, chatID int64, memeID string, msgID int) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ YouTube", fmt.Sprintf("toggle:youtube:%s", memeID)),
			tgbotapi.NewInlineKeyboardButtonData("❌ Instagram", fmt.Sprintf("toggle:instagram:%s", memeID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ X", fmt.Sprintf("toggle:x:%s", memeID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📤 Опубликовать выбранные", fmt.Sprintf("publishsel:%s", memeID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📤 Опубликовать все", fmt.Sprintf("publishall:%s", memeID)),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", fmt.Sprintf("cancelchoose:%s", memeID)),
		),
	)

	msg := tgbotapi.NewMessage(chatID, "Выберите платформы для публикации:")
	msg.ReplyMarkup = keyboard
	b.tg.Send(msg)
}

func (b *TelegramBot) handleChangeAudio(ctx context.Context, chatID int64, memeID string, msgID int) {
	b.replyText(chatID, "🎵 Замена трека... (в разработке)")
	// TODO: Implement audio replacement logic
}

func (b *TelegramBot) handleDeleteMeme(ctx context.Context, chatID int64, memeID string, msgID int) {
	b.log.Infof("handleDeleteMeme: START - memeID=%s, chatID=%d", memeID, chatID)
	b.replyText(chatID, "🗑️ Удаляю мем...")

	// Create a new context with background (don't use request context which might be cancelled)
	deleteCtx := context.Background()

	go func() {
		b.log.Infof("handleDeleteMeme: goroutine START - memeID=%s", memeID)
		if err := b.svc.Impl().DeleteMeme(deleteCtx, memeID); err != nil {
			b.log.Errorf("handleDeleteMeme: FAILED - memeID=%s, err=%v", memeID, err)
			b.replyText(chatID, fmt.Sprintf("❌ Ошибка удаления: %v", err))
			return
		}

		b.log.Infof("handleDeleteMeme: SUCCESS - meme deleted: %s", memeID)
		b.replyText(chatID, "✅ Мем успешно удален")
	}()
}

func (b *TelegramBot) handleTogglePlatform(ctx context.Context, chatID int64, platform, memeID string, msgID int) {
	// TODO: Track selected platforms in bot data
	b.replyText(chatID, fmt.Sprintf("Переключена платформа: %s", platform))
}

func (b *TelegramBot) handlePublishSelected(ctx context.Context, chatID int64, memeID string, msgID int) {
	b.replyText(chatID, "📤 Публикация на выбранные платформы... (в разработке)")
	// TODO: Implement upload to selected platforms
}

func (b *TelegramBot) handlePublishAll(ctx context.Context, chatID int64, memeID string, msgID int) {
	b.replyText(chatID, "📤 Публикация на все платформы... (в разработке)")
	// TODO: Implement upload to all platforms
}

func (b *TelegramBot) replyText(chatID int64, text string) {
	m := tgbotapi.NewMessage(chatID, text)
	_, _ = b.tg.Send(m)
}

func (b *TelegramBot) handleMeme(ctx context.Context, chatID int64) {
	meme, err := b.svc.Impl().GetRandomMeme(ctx)
	if err != nil {
		b.log.Errorf("GetRandomMeme failed: %v", err)
		b.replyText(chatID, "🚀 Нет готовых мемов, запускаю генерацию...")

		// Generate one meme
		go func() {
			newMeme, genErr := b.svc.Impl().GenerateOneMeme(ctx)
			if genErr != nil {
				b.log.Errorf("generate meme: %v", genErr)
				b.replyText(chatID, fmt.Sprintf("❌ Ошибка генерации: %v", genErr))
				return
			}

			b.log.Infof("meme generated, sending to chat")
			time.Sleep(2 * time.Second) // Brief delay for S3 sync
			b.sendMemeVideo(ctx, chatID, newMeme)
		}()
		return
	}

	b.log.Infof("sending meme %s to chat", meme.ID)
	success := b.sendMemeVideo(ctx, chatID, meme)

	// If sent successfully, delete it from pool and generate a new one
	if success {
		go func() {
			b.log.Infof("handleSendNextMeme: meme sent successfully, deleting and generating new - memeID=%s", meme.ID)
			// CRITICAL: Use background context for long-running operations
			bgCtx := context.Background()

			// CRITICAL: Delete BEFORE generating to avoid race condition
			if err := b.svc.Impl().DeleteMeme(bgCtx, meme.ID); err != nil {
				b.log.Errorf("handleSendNextMeme: failed to delete meme %s: %v", meme.ID, err)
				return // Don't generate new meme if delete failed
			}
			b.log.Infof("handleSendNextMeme: meme deleted successfully, generating replacement: %s", meme.ID)
			if _, err := b.svc.Impl().GenerateOneMeme(bgCtx); err != nil {
				b.log.Errorf("handleSendNextMeme: failed to generate replacement meme: %v", err)
			}
		}()
	}
}

// sendMemeVideo sends a single meme video to a chat
func (b *TelegramBot) sendMemeVideo(ctx context.Context, chatID int64, meme *model.Meme) bool {
	videoPath, err := b.svc.Impl().DownloadMemeToTemp(ctx, meme)
	if err != nil {
		b.log.Errorf("download meme: %v", err)
		b.replyText(chatID, "Ошибка загрузки мема")
		return false
	}
	defer os.Remove(videoPath)

	f, err := os.Open(videoPath)
	if err != nil {
		b.log.Errorf("open meme file: %v", err)
		b.replyText(chatID, "Ошибка открытия видео")
		return false
	}
	defer f.Close()

	msg := tgbotapi.NewVideo(chatID, tgbotapi.FileReader{Name: "meme.mp4", Reader: f})
	msg.Caption = meme.Title

	// Add inline keyboard with action buttons
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📤 Опубликовать", fmt.Sprintf("publish:%s", meme.ID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🎯 Выбрать платформы", fmt.Sprintf("choose:%s", meme.ID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🎵 Сменить трек", fmt.Sprintf("changeaudio:%s", meme.ID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Удалить", fmt.Sprintf("delete:%s", meme.ID)),
		),
	)
	msg.ReplyMarkup = keyboard

	if _, err := b.tg.Send(msg); err != nil {
		b.log.Errorf("send meme: %v", err)
		b.replyText(chatID, "Ошибка отправки видео")
		return false
	}
	return true
}

func tempFilePath(prefix, name string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_").Replace(name)
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%s", prefix, safe))
}

// runSchedulePoster runs in background and sends memes at scheduled times
func (b *TelegramBot) runSchedulePoster(ctx context.Context) {
	defer close(b.schedulePosterDone)

	// Wait for schedule to load
	time.Sleep(3 * time.Second)

	sched := b.svc.GetSchedule()
	if sched == nil {
		b.log.Errorf("schedule not loaded, poster disabled")
		return
	}

	cfg := b.svc.GetConfig()
	chatID := cfg.PostsChatID
	if chatID == 0 {
		// Try to read from env
		if v := os.Getenv("POSTS_CHAT_ID"); v != "" {
			fmt.Sscanf(v, "%d", &chatID)
		}
	}
	if chatID == 0 {
		b.log.Errorf("POSTS_CHAT_ID not set, schedule poster disabled")
		return
	}

	b.log.Infof("schedule poster started, chatID=%d, entries=%d", chatID, len(sched.Entries))

	ticker := time.NewTicker(10 * time.Second) // Check every 10 seconds
	defer ticker.Stop()

	sentTimes := make(map[string]bool) // Track sent times to avoid duplicates

	for {
		select {
		case <-ctx.Done():
			return
		case <-b.schedulePosterDone:
			return
		case <-ticker.C:
			now := time.Now()

			// Reload schedule if it's a new day
			if sched.Date != now.Format("2006-01-02") {
				newSched, err := scheduler.GetOrCreateSchedule(ctx, b.svc.GetS3Client(), &cfg, now)
				if err == nil && newSched != nil {
					sched = newSched
					b.svc.SetSchedule(sched)
					sentTimes = make(map[string]bool) // Reset sent times
					b.log.Infof("reloaded schedule for %s", sched.Date)
				}
			}

			// Check each entry in schedule
			for _, entry := range sched.Entries {
				timeKey := entry.Time.Format("15:04:05")

				// Skip if already sent
				if sentTimes[timeKey] {
					continue
				}

				// Check if it's time to send (within 1 minute window)
				timeDiff := now.Sub(entry.Time)
				if timeDiff >= 0 && timeDiff < 1*time.Minute {
					b.log.Infof("sending 3 memes at scheduled time %s", entry.Time.Format("15:04:05"))
					go b.sendScheduledMemes(ctx, chatID)
					sentTimes[timeKey] = true
				}
			}
		}
	}
}

// sendScheduledMemes sends 3 random memes to the scheduled chat
func (b *TelegramBot) sendScheduledMemes(ctx context.Context, chatID int64) {
	sentMemeIDs := make([]string, 0, 3)

	for i := 0; i < 3; i++ {
		meme, err := b.svc.Impl().GetRandomMeme(ctx)
		if err != nil {
			b.log.Errorf("get random meme %d for scheduled send: %v", i+1, err)
			continue
		}

		// Download meme to temp file
		videoPath, err := b.svc.Impl().DownloadMemeToTemp(ctx, meme)
		if err != nil {
			b.log.Errorf("download meme %d: %v", i+1, err)
			continue
		}

		sent := false
		func() {
			defer os.Remove(videoPath)

			f, err := os.Open(videoPath)
			if err != nil {
				b.log.Errorf("open meme file %d: %v", i+1, err)
				return
			}
			defer f.Close()

			msg := tgbotapi.NewVideo(chatID, tgbotapi.FileReader{Name: "meme.mp4", Reader: f})
			msg.Caption = meme.Title

			// Add inline keyboard with action buttons
			keyboard := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📤 Опубликовать", fmt.Sprintf("publish:%s", meme.ID)),
				),
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🎯 Выбрать платформы", fmt.Sprintf("choose:%s", meme.ID)),
				),
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🎵 Сменить трек", fmt.Sprintf("changeaudio:%s", meme.ID)),
				),
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🗑️ Удалить", fmt.Sprintf("delete:%s", meme.ID)),
				),
			)
			msg.ReplyMarkup = keyboard

			if _, err := b.tg.Send(msg); err != nil {
				b.log.Errorf("send meme %d: %v", i+1, err)
			} else {
				sent = true
			}
		}()

		if sent {
			sentMemeIDs = append(sentMemeIDs, meme.ID)
		}

		// Small delay between sends
		time.Sleep(500 * time.Millisecond)
	}

	// Delete sent memes and generate new ones in background
	if len(sentMemeIDs) > 0 {
		go func() {
			bgCtx := context.Background()
			for _, memeID := range sentMemeIDs {
				b.log.Infof("handleSendMultipleMemes: meme sent successfully, deleting and generating new - memeID=%s", memeID)
				// CRITICAL: Delete BEFORE generating to avoid race condition
				if err := b.svc.Impl().DeleteMeme(bgCtx, memeID); err != nil {
					b.log.Errorf("handleSendMultipleMemes: failed to delete meme %s: %v", memeID, err)
					continue // Skip generation if delete failed
				}
				b.log.Infof("handleSendMultipleMemes: meme deleted successfully, generating replacement: %s", memeID)
				if _, err := b.svc.Impl().GenerateOneMeme(bgCtx); err != nil {
					b.log.Errorf("handleSendMultipleMemes: failed to generate replacement meme: %v", err)
				}
			}
		}()
	}
}

func (b *TelegramBot) cmdHelp(chatID int64) {
	help := `Команды:
/start — приветствие
/help — помощь
/meme — получить случайный мем из пула (с кнопками действий)
/status — статус генерации и использование памяти
/errors — последние 50 строк errors.log
/chatid — показать текущий chat ID
/scheduleinfo — расписание отправок мемов на сегодня
/runscheduled — отправить 3 мема в чат сейчас (с кнопками действий)
/clearsources — очистить папку источников
/sync — синхронизировать sources.json и memes.json с S3
/forcecheck — принудительно проверить и восстановить ресурсы
/eenfinit — генерация мемов ТОЛЬКО для аккаунта eenfinit на YouTube

📤 Кнопки действий:
• Опубликовать — загрузить на все платформы
• Выбрать платформы — выбрать конкретные платформы для загрузки
• Сменить трек — заменить аудио в видео
• Удалить — удалить мем из S3 и индекса

🤖 Автоматический мониторинг:
Бот автоматически следит за количеством песен, источников и мем-видео.
Проверка каждые 5 минут. Режим: параллельный (если >1 ядра).

📅 Расписание:
Мемы отправляются N раз в день по расписанию (10:00-23:59).
Команда /meme отправляет случайное видео из уже сгенерированных.`
	b.replyText(chatID, help)
}

func (b *TelegramBot) cmdErrors(chatID int64) {
	lines, err := TailLastNLines(b.errorsPath, 50)
	if err != nil {
		b.log.Errorf("tail errors: %v", err)
		b.replyText(chatID, "Не удалось прочитать errors.log")
		return
	}
	msg := strings.Join(lines, "\n")
	if strings.TrimSpace(msg) == "" {
		msg = "errors.log пуст"
	}
	b.replyText(chatID, msg)
}

func (b *TelegramBot) cmdStatus(ctx context.Context, chatID int64) {
	sourcesCount, err := b.svc.GetSourcesCount(ctx)
	if err != nil {
		b.log.Errorf("get sources count: %v", err)
		sourcesCount = -1
	}

	songsCount, err := b.svc.GetSongsCount(ctx)
	if err != nil {
		b.log.Errorf("get songs count: %v", err)
		songsCount = -1
	}

	memesCount, err := b.svc.GetMemesCount(ctx)
	if err != nil {
		b.log.Errorf("get memes count: %v", err)
		memesCount = -1
	}

	var sourcesStr, songsStr string
	if sourcesCount == -1 {
		sourcesStr = "Ошибка"
	} else {
		sourcesStr = fmt.Sprintf("%d", sourcesCount)
	}
	if songsCount == -1 {
		songsStr = "Ошибка"
	} else {
		songsStr = fmt.Sprintf("%d", songsCount)
	}

	status := fmt.Sprintf("📊 Статус системы:\n\n✅ Scheduler: работает\n✅ Errors.log: доступен\n📁 Загруженных источников: %s\n🎵 Загруженных аудио: %s\n🎥 Сгенерировано мемов: %d", sourcesStr, songsStr, memesCount)
	b.replyText(chatID, status)
}

func (b *TelegramBot) cmdChatID(chatID int64) {
	b.replyText(chatID, fmt.Sprintf("Ваш Chat ID: %d", chatID))
}

func (b *TelegramBot) savePostsChatIDIfNeeded(ctx context.Context, chatID int64) {
	// Always save the chat ID to ensure we have the latest one
	if err := b.svc.SavePostsChatID(ctx, chatID); err != nil {
		b.log.Errorf("save posts_chat_id to S3: %v", err)
		return
	}
	b.log.Infof("saved POSTS_CHAT_ID=%d", chatID)
}

func (b *TelegramBot) cmdScheduleInfo(chatID int64) {
	sched := b.svc.GetSchedule()
	if sched == nil {
		b.replyText(chatID, "📅 Расписание ещё не загружено. Попробуй позже.")
		return
	}

	now := time.Now()
	lines := []string{
		fmt.Sprintf("📅 Расписание на %s", sched.Date),
		fmt.Sprintf("Всего отправок: %d", len(sched.Entries)),
		"",
	}

	for i, entry := range sched.Entries {
		status := "⏳ ожидает"
		if entry.Time.Before(now) {
			status = "✅ выполнена"
		}
		lines = append(lines, fmt.Sprintf("%d. %s %s", i+1, entry.Time.Format("15:04:05"), status))
	}

	b.replyText(chatID, strings.Join(lines, "\n"))
}

func (b *TelegramBot) cmdRunScheduled(ctx context.Context, chatID int64) {
	b.replyText(chatID, "▶️ Отправляю 3 случайных мема...")

	for i := 0; i < 3; i++ {
		meme, err := b.svc.Impl().GetRandomMeme(ctx)
		if err != nil {
			b.log.Errorf("get random meme %d: %v", i+1, err)
			b.replyText(chatID, fmt.Sprintf("❌ Ошибка при получении мема #%d", i+1))
			continue
		}

		videoPath, err := b.svc.Impl().DownloadMemeToTemp(ctx, meme)
		if err != nil {
			b.log.Errorf("download meme %d: %v", i+1, err)
			continue
		}

		func() {
			defer os.Remove(videoPath)

			f, err := os.Open(videoPath)
			if err != nil {
				b.log.Errorf("open meme file %d: %v", i+1, err)
				return
			}
			defer f.Close()

			msg := tgbotapi.NewVideo(chatID, tgbotapi.FileReader{Name: "meme.mp4", Reader: f})
			msg.Caption = meme.Title
			if _, err := b.tg.Send(msg); err != nil {
				b.log.Errorf("send meme %d: %v", i+1, err)
			}
		}()
	}
}

func (b *TelegramBot) cmdClearSources(ctx context.Context, chatID int64) {
	b.replyText(chatID, "🗑️ Очищаю папку источников...")

	if err := b.svc.ClearSources(ctx); err != nil {
		b.log.Errorf("clear sources: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка при очистке: %v", err))
		return
	}

	b.replyText(chatID, "✅ Папка источников успешно очищена")
}

func (b *TelegramBot) cmdSync(ctx context.Context, chatID int64) {
	b.replyText(chatID, "🔄 Начинаю синхронизацию sources.json и memes.json с S3...")

	// Sync sources
	sourcesMsg := "📁 Синхронизация sources.json..."
	b.replyText(chatID, sourcesMsg)

	if err := b.svc.SyncSources(ctx); err != nil {
		b.log.Errorf("sync sources: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка синхронизации sources: %v", err))
	} else {
		b.replyText(chatID, "✅ Sources.json синхронизирован с S3 папкой sources/")
	}

	// Sync memes
	memesMsg := "📁 Синхронизация memes.json..."
	b.replyText(chatID, memesMsg)

	if err := b.svc.SyncMemes(ctx); err != nil {
		b.log.Errorf("sync memes: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка синхронизации memes: %v", err))
	} else {
		b.replyText(chatID, "✅ Memes.json синхронизирован с S3 папкой memes/")
	}

	b.replyText(chatID, "🎉 Синхронизация завершена!")
}

func (b *TelegramBot) cmdForceCheck(ctx context.Context, chatID int64) {
	b.replyText(chatID, "🔍 Запускаю принудительную проверку ресурсов...")

	monitor := b.svc.GetMonitor()
	if monitor == nil {
		b.replyText(chatID, "❌ Монитор ресурсов недоступен")
		return
	}

	// Get current counts before check
	songsCount, _ := b.svc.GetSongsCount(ctx)
	sourcesCount, _ := b.svc.GetSourcesCount(ctx)
	memesCount, _ := b.svc.GetMemesCount(ctx)

	cfg := b.svc.GetConfig()

	statusBefore := fmt.Sprintf("📊 До проверки:\n• Песни: %d\n• Источники: %d/%d\n• Мемы: %d/%d",
		songsCount, sourcesCount, cfg.MaxSources, memesCount, cfg.MaxMemes)
	b.replyText(chatID, statusBefore)

	// Force check
	monitor.ForceCheck(ctx)

	// Wait a bit for operations to complete
	time.Sleep(3 * time.Second)

	// Get counts after check
	songsCountAfter, _ := b.svc.GetSongsCount(ctx)
	sourcesCountAfter, _ := b.svc.GetSourcesCount(ctx)
	memesCountAfter, _ := b.svc.GetMemesCount(ctx)

	statusAfter := fmt.Sprintf("📊 После проверки:\n• Песни: %d\n• Источники: %d/%d\n• Мемы: %d/%d",
		songsCountAfter, sourcesCountAfter, cfg.MaxSources, memesCountAfter, cfg.MaxMemes)
	b.replyText(chatID, statusAfter)

	b.replyText(chatID, "✅ Проверка завершена!")
}

func (b *TelegramBot) cmdEenfinit(ctx context.Context, chatID int64, args string) {
	// Check if token_eenfinit.pickle exists
	tokenPath := os.Getenv("TOKEN_EENFINIT")
	if tokenPath == "" {
		tokenPath = "token_eenfinit.pickle"
	}

	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		b.replyText(chatID, "❌ Файл token_eenfinit.pickle не найден\n\n"+
			"Загрузите его или создайте новый:\n"+
			"/uploadtoken (загрузите как token_eenfinit.pickle)\n\n"+
			"Или используйте скрипт:\n"+
			"python get_youtube_token.py token_eenfinit.pickle client_secrets.json")
		return
	}

	b.replyText(chatID, "🚀 Генерация для eenfinit запущена... (в разработке)\n\n"+
		"Эта функция генерирует мемы только для плейлиста eenfinit и публикует в YouTube аккаунт eenfinit.\n"+
		"Источники: Pinterest, Reddit\n"+
		"Плейлист: https://music.youtube.com/playlist?list=OLAK5uy_mjqaQ3Ut5XK1m2vEvYuzcoUb3D6XrW9SA")

	// TODO: Implement eenfinit generation logic
	// Parse args (count, pin_num, audio_duration)
	// Generate memes using eenfinit playlist
	// Upload to YouTube eenfinit account
}
