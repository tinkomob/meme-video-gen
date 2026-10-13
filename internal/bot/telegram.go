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
	uploaders_types "meme-video-gen/internal/uploaders"
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
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := b.tg.GetUpdatesChan(u)
	b.log.Infof("telegram bot started as @%s", b.tg.Self.UserName)

	go b.runBestOfPoster(ctx)
	go b.runTeaserPoster(ctx)

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
		b.replyText(chatID, "Привет! Я бот для музыкальных подборок. Наберите /help для списка команд.")
	case "help":
		b.cmdHelp(chatID)
	case "chatid":
		b.cmdChatID(chatID)
	case "song":
		b.cmdSong(ctx, chatID, msg.CommandArguments())
	default:
		b.replyText(chatID, "Неизвестная команда. Используйте /help")
	}
}

func (b *TelegramBot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	b.tg.Send(tgbotapi.NewCallback(cb.ID, ""))

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

func (b *TelegramBot) cmdSetMixtapes(ctx context.Context, chatID int64, args string) {
	parts := strings.Fields(args)
	if len(parts) == 0 {
		b.replyText(chatID, "Использование: /setmixtapes bestof [...] | /setmixtapes teaser [...]")
		return
	}
	if parts[0] != "bestof" && parts[0] != "teaser" {
		b.replyText(chatID, "Доступно управление Best Of и тизером: /setmixtapes bestof | teaser")
		return
	}
	b.cmdSetMixtapesEngagement(ctx, chatID, parts[0], parts[1:])
}

func (b *TelegramBot) cmdSetMixtapesEngagement(ctx context.Context, chatID int64, sub string, rest []string) {
	cfg := b.svc.GetConfig()
	ec := b.svc.GetEngagementConfig()

	save := func() bool {
		if err := scheduler.SaveEngagementConfig(ctx, b.svc.GetS3Client(), &cfg, ec); err != nil {
			b.replyText(chatID, fmt.Sprintf("❌ Ошибка сохранения: %v", err))
			return false
		}
		b.svc.SetEngagementConfig(ec)
		return true
	}

	switch sub {
	case "bestof":
		if len(rest) == 0 {
			artists := strings.Join(ec.BestOf.Artists, ", ")
			if artists == "" {
				artists = "(none)"
			}
			status := "выкл"
			if ec.BestOf.Enabled {
				status = "вкл"
			}
			var nextPost string
			if !ec.BestOf.LastPostedAt.IsZero() {
				loc2, err2 := time.LoadLocation("Asia/Tomsk")
				if err2 != nil {
					loc2 = time.FixedZone("UTC+7", 7*3600)
				}
				nextPost = ec.BestOf.LastPostedAt.In(loc2).Add(time.Duration(ec.BestOf.IntervalDays)*24*time.Hour).Format("02.01 15:04") + " (Tomsk)"
			} else {
				nextPost = "при первом запуске"
			}
			b.replyText(chatID, fmt.Sprintf(
				"🎤 Best Of — %s\nАртисты: %s\nСегментов: %d\nИнтервал: каждые %d дня\nСледующий: %s\n\n/setmixtapes bestof on|off\n/setmixtapes bestof add <artist>\n/setmixtapes bestof remove <artist>",
				status, artists, ec.BestOf.SegmentCount, ec.BestOf.IntervalDays, nextPost,
			))
			return
		}
		switch rest[0] {
		case "on":
			ec.BestOf.Enabled = true
			if save() {
				b.replyText(chatID, "✅ Best Of включён")
			}
		case "off":
			ec.BestOf.Enabled = false
			if save() {
				b.replyText(chatID, "✅ Best Of выключен")
			}
		case "add":
			if len(rest) < 2 {
				b.replyText(chatID, "Использование: /setmixtapes bestof add <artist>")
				return
			}
			artist := strings.Join(rest[1:], " ")
			for _, a := range ec.BestOf.Artists {
				if strings.EqualFold(a, artist) {
					b.replyText(chatID, "Артист уже в списке")
					return
				}
			}
			ec.BestOf.Artists = append(ec.BestOf.Artists, artist)
			if save() {
				b.replyText(chatID, fmt.Sprintf("✅ Артист %q добавлен", artist))
			}
		case "remove":
			if len(rest) < 2 {
				b.replyText(chatID, "Использование: /setmixtapes bestof remove <artist>")
				return
			}
			artist := strings.Join(rest[1:], " ")
			var kept []string
			found := false
			for _, a := range ec.BestOf.Artists {
				if strings.EqualFold(a, artist) {
					found = true
					continue
				}
				kept = append(kept, a)
			}
			if !found {
				b.replyText(chatID, "Артист не найден в списке")
				return
			}
			ec.BestOf.Artists = kept
			if save() {
				b.replyText(chatID, fmt.Sprintf("✅ Артист %q удалён", artist))
			}
		default:
			b.replyText(chatID, "Использование: /setmixtapes bestof [on|off|add <artist>|remove <artist>]")
		}

	case "teaser":
		if len(rest) == 0 {
			status := "выкл"
			if ec.Teaser.Enabled {
				status = "вкл"
			}
			b.replyText(chatID, fmt.Sprintf(
				"🎵 Wanna Know Teaser — %s\nВремя: %02d:%02d (Tomsk)\n\n/setmixtapes teaser on|off\n/setmixtapes teaser time HH:MM",
				status, ec.Teaser.Hour, ec.Teaser.Minute,
			))
			return
		}
		switch rest[0] {
		case "on":
			ec.Teaser.Enabled = true
			if save() {
				b.replyText(chatID, "✅ Teaser включён")
			}
		case "off":
			ec.Teaser.Enabled = false
			if save() {
				b.replyText(chatID, "✅ Teaser выключен")
			}
		case "time":
			if len(rest) < 2 {
				b.replyText(chatID, "Использование: /setmixtapes teaser time HH:MM")
				return
			}
			timeParts := strings.Split(rest[1], ":")
			if len(timeParts) != 2 {
				b.replyText(chatID, "❌ Неверный формат HH:MM")
				return
			}
			var hour, min int
			if _, err := fmt.Sscanf(timeParts[0], "%d", &hour); err != nil || hour < 0 || hour > 23 {
				b.replyText(chatID, "❌ Неверный час")
				return
			}
			if _, err := fmt.Sscanf(timeParts[1], "%d", &min); err != nil || min < 0 || min > 59 {
				b.replyText(chatID, "❌ Неверные минуты")
				return
			}
			ec.Teaser.Hour = hour
			ec.Teaser.Minute = min
			if save() {
				b.replyText(chatID, fmt.Sprintf("✅ Teaser время: %02d:%02d", hour, min))
			}
		default:
			b.replyText(chatID, "Использование: /setmixtapes teaser [on|off|time HH:MM]")
		}

	default:
		b.replyText(chatID, "Использование: /setmixtapes bestof [...] | /setmixtapes teaser [...]")
	}
}

// runBestOfPoster checks every hour whether a "Best of [Artist]" video is due and posts it.
func (b *TelegramBot) runBestOfPoster(ctx context.Context) {
	time.Sleep(10 * time.Second)

	cfg := b.svc.GetConfig()
	chatID := cfg.PostsChatID
	if chatID == 0 {
		if v := os.Getenv("POSTS_CHAT_ID"); v != "" {
			fmt.Sscanf(v, "%d", &chatID)
		}
	}
	if chatID == 0 {
		b.log.Errorf("POSTS_CHAT_ID not set, best-of poster disabled")
		return
	}

	checkAndSend := func() {
		ec := b.svc.GetEngagementConfig()
		if !ec.BestOf.Enabled || len(ec.BestOf.Artists) == 0 {
			return
		}
		intervalDays := ec.BestOf.IntervalDays
		if intervalDays <= 0 {
			intervalDays = 3
		}
		if !ec.BestOf.LastPostedAt.IsZero() && time.Since(ec.BestOf.LastPostedAt) < time.Duration(intervalDays)*24*time.Hour {
			return
		}
		go b.sendBestOfMixtape(ctx, chatID)
	}

	// Check immediately at startup (in case the bot was restarted and a send is overdue).
	checkAndSend()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkAndSend()
		}
	}
}

// sendBestOfMixtape generates and posts a "Best of [Artist]" compilation.
func (b *TelegramBot) sendBestOfMixtape(ctx context.Context, chatID int64) {
	ec := b.svc.GetEngagementConfig()
	if len(ec.BestOf.Artists) == 0 {
		return
	}

	artist := ec.BestOf.Artists[ec.BestOf.NextArtistIdx%len(ec.BestOf.Artists)]
	segCount := ec.BestOf.SegmentCount
	if segCount <= 0 {
		segCount = 5
	}

	b.log.Infof("sendBestOfMixtape: generating Best of %q (%d segments)", artist, segCount)
	gen := b.svc.GetMixtapeGenerator()
	m, err := gen.GenerateBestOf(ctx, artist, segCount)
	if err != nil {
		b.log.Errorf("sendBestOfMixtape: generate: %v", err)
		return
	}

	videoPath, err := gen.DownloadVideoToTemp(ctx, m)
	if err != nil {
		b.log.Errorf("sendBestOfMixtape: download: %v", err)
		_ = gen.Delete(ctx, m.ID)
		return
	}
	defer os.Remove(videoPath)

	tomsk := time.FixedZone("Asia/Tomsk", 7*3600)
	silent := time.Now().In(tomsk).Hour() < 10

	mgr := b.svc.GetUploadersManager()
	if mgr == nil {
		b.log.Errorf("sendBestOfMixtape: uploaders manager is nil")
		b.replyHTMLSilent(chatID, "❌ Best Of: менеджер загрузчиков не инициализирован", silent)
		_ = gen.Delete(ctx, m.ID)
		return
	}
	if _, err := mgr.GetUploader("youtube"); err != nil {
		if err := b.svc.InitializeYouTubeUploaderFromS3(ctx); err != nil {
			b.log.Warnf("sendBestOfMixtape: failed to load YouTube uploader from S3: %v", err)
		}
	}

	caption := m.Title + "\n\n"
	for i, t := range m.Titles {
		if i < len(m.Authors) {
			caption += fmt.Sprintf("%d. %s — %s\n", i+1, m.Authors[i], t)
		}
	}
	uploadReq := &uploaders_types.UploadRequest{
		VideoPath:   videoPath,
		Title:       m.Title,
		Caption:     caption,
		Description: caption,
		Privacy:     "public",
		Silent:      silent,
	}
	results := mgr.UploadToAll(ctx, uploadReq)
	success := 0
	var lines []string
	esc := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
	)
	for platform, r := range results {
		p := esc.Replace(strings.ToUpper(platform))
		if r.Success {
			success++
			if r.URL != "" {
				url := esc.Replace(r.URL)
				lines = append(lines, fmt.Sprintf("✅ %s: <a href=\"%s\">смотреть</a>", p, url))
			} else {
				lines = append(lines, fmt.Sprintf("✅ %s: загружено", p))
			}
		} else {
			lines = append(lines, fmt.Sprintf("❌ %s: %s", p, esc.Replace(r.Error)))
		}
	}
	if success > 0 {
		b.replyHTMLSilent(chatID, fmt.Sprintf("🎤 Best Of опубликован:\n\n%s", strings.Join(lines, "\n")), silent)
	} else {
		b.replyHTMLSilent(chatID, fmt.Sprintf("❌ Best Of не удалось опубликовать:\n%s", strings.Join(lines, "\n")), silent)
	}

	_ = gen.Delete(ctx, m.ID)

	// Update engagement config: advance artist index and record post time
	ec = b.svc.GetEngagementConfig()
	ec.BestOf.LastPostedAt = time.Now()
	ec.BestOf.NextArtistIdx = (ec.BestOf.NextArtistIdx + 1) % len(ec.BestOf.Artists)
	cfg := b.svc.GetConfig()
	_ = scheduler.SaveEngagementConfig(ctx, b.svc.GetS3Client(), &cfg, ec)
	b.svc.SetEngagementConfig(ec)
	b.log.Infof("sendBestOfMixtape: done, next artist idx=%d", ec.BestOf.NextArtistIdx)
}

// runTeaserPoster checks every 10 seconds whether the daily "Wanna know?" teaser is due.
func (b *TelegramBot) runTeaserPoster(ctx context.Context) {
	time.Sleep(15 * time.Second)

	cfg := b.svc.GetConfig()
	chatID := cfg.PostsChatID
	if chatID == 0 {
		if v := os.Getenv("POSTS_CHAT_ID"); v != "" {
			fmt.Sscanf(v, "%d", &chatID)
		}
	}
	if chatID == 0 {
		b.log.Errorf("POSTS_CHAT_ID not set, teaser poster disabled")
		return
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	tomsk := time.FixedZone("Asia/Tomsk", 7*3600)
	var running atomic.Bool

	teaserDue := func() bool {
		ec := b.svc.GetEngagementConfig()
		if !ec.Teaser.Enabled {
			return false
		}
		now := time.Now().In(tomsk)
		today := now.Format("2006-01-02")
		// Already sent today
		if !ec.Teaser.LastPostedAt.IsZero() && ec.Teaser.LastPostedAt.In(tomsk).Format("2006-01-02") == today {
			return false
		}
		// Scheduled time has arrived (or already passed today)
		scheduled := time.Date(now.Year(), now.Month(), now.Day(), ec.Teaser.Hour, ec.Teaser.Minute, 0, 0, tomsk)
		return !now.Before(scheduled)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if teaserDue() && running.CompareAndSwap(false, true) {
				// Mark as posted immediately so re-checks in the next 10s don't fire again.
				ec := b.svc.GetEngagementConfig()
				ec.Teaser.LastPostedAt = time.Now()
				cfg2 := b.svc.GetConfig()
				_ = scheduler.SaveEngagementConfig(ctx, b.svc.GetS3Client(), &cfg2, ec)
				b.svc.SetEngagementConfig(ec)
				go func() {
					defer running.Store(false)
					b.sendWannaKnowTeaser(ctx, chatID)
				}()
			}
		}
	}
}

// sendWannaKnowTeaser generates a single-segment teaser and posts it.
func (b *TelegramBot) sendWannaKnowTeaser(ctx context.Context, chatID int64) {
	b.log.Infof("sendWannaKnowTeaser: generating teaser")
	gen := b.svc.GetMixtapeGenerator()
	m, err := gen.GenerateTeaser(ctx)
	if err != nil {
		b.log.Errorf("sendWannaKnowTeaser: generate: %v", err)
		return
	}

	videoPath, err := gen.DownloadVideoToTemp(ctx, m)
	if err != nil {
		b.log.Errorf("sendWannaKnowTeaser: download: %v", err)
		_ = gen.Delete(ctx, m.ID)
		return
	}
	defer os.Remove(videoPath)

	tomsk := time.FixedZone("Asia/Tomsk", 7*3600)
	silent := time.Now().In(tomsk).Hour() < 10

	caption := ""
	if len(m.Authors) > 0 && len(m.Titles) > 0 {
		caption = fmt.Sprintf("%s — %s", m.Authors[0], m.Titles[0])
	}
	if len(m.SongIDs) > 0 {
		if song, err := b.svc.GetSongByID(ctx, m.SongIDs[0]); err == nil && song.SourceURL != "" {
			caption += "\n🔗 " + song.SourceURL
		}
	}

	mgr := b.svc.GetUploadersManager()
	if mgr == nil {
		b.log.Errorf("sendWannaKnowTeaser: uploaders manager is nil")
		b.replyHTMLSilent(chatID, "❌ Teaser: менеджер загрузчиков не инициализирован", silent)
		_ = gen.Delete(ctx, m.ID)
		return
	}
	if _, err := mgr.GetUploader("youtube"); err != nil {
		if err := b.svc.InitializeYouTubeUploaderFromS3(ctx); err != nil {
			b.log.Warnf("sendWannaKnowTeaser: failed to load YouTube uploader from S3: %v", err)
		}
	}

	uploadReq := &uploaders_types.UploadRequest{
		VideoPath:   videoPath,
		Title:       m.Title,
		Caption:     caption,
		Description: caption,
		Privacy:     "public",
		Silent:      silent,
	}
	results := mgr.UploadToAll(ctx, uploadReq)
	success := 0
	var lines []string
	esc := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
	)
	for platform, r := range results {
		p := esc.Replace(strings.ToUpper(platform))
		if r.Success {
			success++
			if r.URL != "" {
				url := esc.Replace(r.URL)
				lines = append(lines, fmt.Sprintf("✅ %s: <a href=\"%s\">смотреть</a>", p, url))
			} else {
				lines = append(lines, fmt.Sprintf("✅ %s: загружено", p))
			}
		} else {
			lines = append(lines, fmt.Sprintf("❌ %s: %s", p, esc.Replace(r.Error)))
		}
	}
	if success > 0 {
		b.replyHTMLSilent(chatID, fmt.Sprintf("🎵 Teaser опубликован:\n\n%s", strings.Join(lines, "\n")), silent)
	} else {
		b.replyHTMLSilent(chatID, fmt.Sprintf("❌ Teaser не удалось опубликовать:\n%s", strings.Join(lines, "\n")), silent)
	}

	_ = gen.Delete(ctx, m.ID)
	b.log.Infof("sendWannaKnowTeaser: done")
}

// getCachedOrDownloadMeme retrieves cached meme file or downloads from S3 with caching
// Optimization: avoids re-downloading same meme multiple times within TTL (default 5 minutes)
// This significantly reduces S3 egress traffic during scheduled posting
func (b *TelegramBot) cmdHelp(chatID int64) {
	help := `Команды:
/start — приветствие
/help — помощь
/songs — загрузить новые треки в S3 (songs/)
/song [query] — скачать трек в формате MP3 или MP4A
              /song — выбрать из списка, случайный или поиск
              /song Dua Lipa — найти треки Dua Lipa и скачать
/setmixtapes bestof — управление Best Of [Artist] (каждые N дней)
              /setmixtapes bestof on/off — включить/выключить
              /setmixtapes bestof add <artist> — добавить артиста
              /setmixtapes bestof remove <artist> — удалить артиста
/setmixtapes teaser — управление ежедневным тизером "Wanna know this song?"
              /setmixtapes teaser on/off — включить/выключить
              /setmixtapes teaser time HH:MM — изменить время отправки
/status — статус генерации и использование памяти
/errors — скачать файл errors.log с последними ошибками
/chatid — показать текущий chat ID
/checkfiles — проверить наличие и размер файлов (token.pickle, client_secrets.json)
/uploadtoken — загрузить token.pickle как документ
/uploadclient — загрузить client_secrets.json как документ
/syncfiles — загрузить все файлы в S3
/downloadfiles — загрузить все файлы из S3 локально

Этот бот публикует Best Of и ежедневный тизер "Wanna know this song?".`
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

// mixtapeText builds the shared title + numbered song list used for YouTube and Telegram.
// YouTube uses Title=m.Title and Description=mixtapeText; Telegram caption = mixtapeText.
