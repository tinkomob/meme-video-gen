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
	default:
		b.replyText(chatID, "Неизвестная команда. Используйте /help")
	}
}

func (b *TelegramBot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	// Stub for future callback handling (publish, choose platforms, etc.)
	b.tg.Send(tgbotapi.NewCallback(cb.ID, "Callback обработка пока не реализована"))
}

func (b *TelegramBot) replyText(chatID int64, text string) {
	m := tgbotapi.NewMessage(chatID, text)
	_, _ = b.tg.Send(m)
}

func (b *TelegramBot) handleMeme(ctx context.Context, chatID int64) {
	meme, err := b.svc.Impl().GetRandomMeme(ctx)
	if err != nil {
		b.log.Errorf("get random meme: %v", err)
		b.replyText(chatID, "Мемы ещё не сгенерированы. Подожди пару минут.")
		return
	}

	b.sendMemeVideo(ctx, chatID, meme)
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
	for i := 0; i < 3; i++ {
		meme, err := b.svc.Impl().GetRandomMeme(ctx)
		if err != nil {
			b.log.Errorf("get random meme %d for scheduled send: %v", i+1, err)
			continue
		}

		if !b.sendMemeVideo(ctx, chatID, meme) {
			b.log.Errorf("failed to send meme %d to scheduled chat", i+1)
		}

		// Small delay between sends
		time.Sleep(500 * time.Millisecond)
	}
}

func (b *TelegramBot) cmdHelp(chatID int64) {
	help := `Команды:
/start — приветствие
/help — помощь
/meme — получить случайный мем из пула
/status — статус генерации и использование памяти
/errors — последние 50 строк errors.log
/chatid — показать текущий chat ID
/scheduleinfo — расписание отправок мемов на сегодня
/runscheduled — отправить 3 мема в чат сейчас

Бот работает в режиме расписания: мемы отправляются N раз в день.
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
	// Stub: show scheduler status, memory usage, etc.
	b.replyText(chatID, "📊 Статус системы:\n\nScheduler: работает\nErrors.log: доступен\nПамять: N/A")
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
