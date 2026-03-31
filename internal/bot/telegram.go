package bot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"meme-video-gen/internal/ai"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/scheduler"
	uploaders_types "meme-video-gen/internal/uploaders"
	"meme-video-gen/internal/video"
)

type TelegramBot struct {
	tg         *tgbotapi.BotAPI
	svc        *scheduler.Service
	log        *logging.Logger
	errorsPath string

	// cancelFunc stops the entire application (used by memory watcher for emergency shutdown)
	cancelFunc context.CancelFunc

	// Schedule poster goroutine control
	schedulePosterDone chan struct{}

	// Cache for slider memes (chatID -> memes)
	sliderMemes map[int64][]*model.Meme

	// S3 bucket name for storing uploaded files
	s3BucketDir string

	// Track search state (chatID -> is searching)
	trackSearchState     map[int64]bool
	trackSearchMux       sync.Mutex
	trackSearchTimestamp map[int64]time.Time // TTL entry: when search mode was set

	// Track search mode (chatID -> "idea" or "song")
	trackSearchMode    map[int64]string
	trackSearchModeMux sync.Mutex

	// Meme file cache to avoid re-downloading (memeID -> file path)
	// Optimization: avoid re-downloading same video multiple times within TTL
	memeFileCache        map[string]string    // memeID -> file path
	memeCacheTTL         map[string]time.Time // memeID -> expiration time
	memeCacheMux         sync.RWMutex
	memeCacheTTLDuration time.Duration // default 5 minutes

	// sliderMemes TTL: evict stale entries if user never publishes
	sliderMemesTTL map[int64]time.Time
	sliderMux      sync.Mutex // guards sliderMemes and sliderMemesTTL
}

func NewTelegramBot(svc *scheduler.Service, log *logging.Logger, errorsPath string, cancel context.CancelFunc) (*TelegramBot, error) {
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
		cancelFunc:           cancel,
		tg:                   api,
		svc:                  svc,
		log:                  log,
		errorsPath:           errorsPath,
		schedulePosterDone:   make(chan struct{}),
		sliderMemes:          make(map[int64][]*model.Meme),
		sliderMemesTTL:       make(map[int64]time.Time),
		s3BucketDir:          "bot-uploads",
		trackSearchState:     make(map[int64]bool),
		trackSearchTimestamp: make(map[int64]time.Time),
		trackSearchMode:      make(map[int64]string),
		memeFileCache:        make(map[string]string),
		memeCacheTTL:         make(map[string]time.Time),
		memeCacheTTLDuration: 5 * time.Minute,
	}, nil
}

func (b *TelegramBot) Run(ctx context.Context) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := b.tg.GetUpdatesChan(u)
	b.log.Infof("telegram bot started as @%s", b.tg.Self.UserName)

	// Start schedule poster goroutine
	go b.runSchedulePoster(ctx)

	// Start background maintenance ticker: cleans up expired caches every 2 minutes.
	go b.runMaintenanceTicker(ctx)

	// Start memory leak watcher.
	go b.runMemoryWatcher(ctx)

	for {
		select {
		case <-ctx.Done():
			// Never block on shutdown: close channel once.
			select {
			case <-b.schedulePosterDone:
				// already closed
			default:
				close(b.schedulePosterDone)
			}
			return nil
		case upd := <-updates:
			if upd.Message != nil && upd.Message.IsCommand() {
				b.handleCommand(ctx, upd.Message)
			} else if upd.Message != nil && upd.Message.Document != nil {
				b.handleDocument(ctx, upd.Message)
			} else if upd.Message != nil && upd.Message.Text != "" {
				// Check if user is in track search mode
				chatID := upd.Message.Chat.ID
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
					} else {
						b.handleIdeaSearch(ctx, chatID, upd.Message.Text)
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
		b.handleMeme(ctx, chatID, msg.CommandArguments())
	case "idea":
		b.cmdIdea(ctx, chatID, msg.CommandArguments())
	case "status":
		b.cmdStatus(ctx, chatID)
	case "chatid":
		b.cmdChatID(chatID)
	case "scheduleinfo":
		b.cmdScheduleInfo(chatID)
	case "setnext":
		b.cmdSetNext(ctx, chatID, msg.CommandArguments())
	case "runscheduled":
		b.cmdRunScheduled(ctx, chatID)
	case "clearschedule":
		b.cmdClearSchedule(ctx, chatID)
	case "clearsources":
		b.cmdClearSources(ctx, chatID)
	case "clearmemes":
		b.cmdClearMemes(ctx, chatID)
	case "sync":
		b.cmdSync(ctx, chatID)
	case "forcecheck":
		b.cmdForceCheck(ctx, chatID)
	case "checkfiles":
		b.cmdCheckFiles(chatID)
	case "uploadtoken":
		b.cmdUploadToken(chatID)
	case "uploadclient":
		b.cmdUploadClient(chatID)
	case "syncfiles":
		b.cmdSyncFiles(ctx, chatID)
	case "downloadfiles":
		b.cmdDownloadFiles(ctx, chatID)
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
	if data == "ideasearch" {
		// Set flag to wait for text input
		b.trackSearchMux.Lock()
		b.trackSearchState[chatID] = true
		b.trackSearchTimestamp[chatID] = time.Now()
		b.trackSearchMux.Unlock()

		b.trackSearchModeMux.Lock()
		b.trackSearchMode[chatID] = "idea"
		b.trackSearchModeMux.Unlock()

		b.replyText(chatID, "🔍 Введите название трека или артиста для поиска:\n(например: Dua Lipa или The Weeknd)")
		return
	}

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
	case "dislike":
		b.handleDislike(ctx, chatID, memeID, cb.Message.MessageID)
	case "dislikeslider":
		b.handleDislikeSlider(ctx, chatID, cb.Message.MessageID)
	case "ideagen":
		// memeID here is actually songID
		b.handleIdeaGeneration(ctx, chatID, memeID)
	case "idealist":
		// memeID here is actually offset
		offset := 0
		fmt.Sscanf(memeID, "%d", &offset)
		b.handleIdeaList(ctx, chatID, offset)
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
	case "selectmeme":
		b.handleSelectMeme(ctx, chatID, memeID, cb.Message.MessageID)
	case "songlist":
		// memeID here is actually offset
		offset := 0
		fmt.Sscanf(memeID, "%d", &offset)
		b.handleSongList(ctx, chatID, offset)
	case "songdl":
		// memeID here is actually songID
		b.handleSongDownload(ctx, chatID, memeID)
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
	go func() {
		b.log.Infof("handlePublish: START - memeID=%s, chatID=%d", memeID, chatID)

		// Send status message "Publishing..."
		statusMsgID := b.replyText(chatID, "⏳ Публикую на все платформы...")

		// Initialize YouTube uploader from S3 if not already done
		if _, err := b.svc.GetUploadersManager().GetUploader("youtube"); err != nil {
			b.log.Infof("handlePublish: YouTube uploader not found, attempting to load from S3")
			if err := b.svc.InitializeYouTubeUploaderFromS3(context.Background()); err != nil {
				b.log.Warnf("handlePublish: failed to load YouTube uploader from S3: %v", err)
			} else {
				b.log.Infof("handlePublish: YouTube uploader loaded successfully from S3")
			}
		} else {
			b.log.Infof("handlePublish: YouTube uploader already initialized")
		}

		// Get meme from storage
		meme, err := b.svc.GetMemeByID(context.Background(), memeID)
		if err != nil {
			b.log.Errorf("handlePublish: failed to get meme: %v", err)
			b.editMessageHTML(chatID, statusMsgID, fmt.Sprintf("❌ Ошибка: мем не найден - %v", err))
			return
		}

		// Download video and thumbnail from S3
		videoPath, err := b.svc.Impl().DownloadMemeToTemp(context.Background(), meme)
		if err != nil {
			b.log.Errorf("handlePublish: failed to download video: %v", err)
			b.editMessageHTML(chatID, statusMsgID, fmt.Sprintf("❌ Ошибка загрузки видео: %v", err))
			return
		}
		defer os.Remove(videoPath)

		// Download thumbnail
		thumbPath, err := b.svc.DownloadFileToTemp(context.Background(), meme.ThumbKey, "thumb")
		if err != nil {
			b.log.Warnf("handlePublish: failed to download thumbnail: %v (continuing without thumb)", err)
			thumbPath = ""
		}
		if thumbPath != "" {
			defer os.Remove(thumbPath)
		}

		// Prepare upload request
		uploaders := b.svc.GetUploadersManager()
		if uploaders == nil {
			b.log.Errorf("handlePublish: uploaders manager is nil")
			b.editMessageHTML(chatID, statusMsgID, "❌ Ошибка: менеджер загрузчиков не инициализирован")
			return
		}

		uploadReq := &uploaders_types.UploadRequest{
			VideoPath:     videoPath,
			ThumbnailPath: thumbPath,
			Title:         meme.Title,
			Description:   ai.GetRandomFact(context.Background()),
			Caption:       meme.Title,
			Privacy:       "public",
		}

		// Upload to all platforms
		b.log.Infof("handlePublish: uploading to all platforms")
		results := uploaders.UploadToAll(context.Background(), uploadReq)

		// Build result message
		success := 0
		failed := 0
		var resultLines []string

		for platform, result := range results {
			if result.Success {
				success++
				if result.URL != "" {
					resultLines = append(resultLines, fmt.Sprintf("✅ %s: <a href=\"%s\">смотреть</a>", strings.ToUpper(platform), result.URL))
				} else {
					resultLines = append(resultLines, fmt.Sprintf("✅ %s: загружено", strings.ToUpper(platform)))
				}
				b.log.Infof("handlePublish: ✓ %s uploaded successfully", platform)
			} else {
				failed++
				resultLines = append(resultLines, fmt.Sprintf("❌ %s: %s", strings.ToUpper(platform), result.Error))
				b.log.Errorf("handlePublish: ✗ %s failed: %s", platform, result.Error)
				if len(result.Details) > 0 {
					for k, v := range result.Details {
						b.log.Errorf("handlePublish: ✗ %s detail %s: %s", platform, k, v)
					}
				}
			}
		}

		// Build final message with all information
		var finalMsg string
		if success > 0 {
			b.log.Infof("handlePublish: COMPLETE - success=%d, failed=%d", success, failed)

			// Delete meme from S3 after successful publish
			b.log.Infof("handlePublish: deleting meme from S3 after publish: %s", memeID)
			deleteErr := b.svc.Impl().DeleteMeme(context.Background(), memeID)

			deleteStatus := ""
			if deleteErr != nil {
				b.log.Errorf("handlePublish: failed to delete meme from S3: %v", deleteErr)
				deleteStatus = fmt.Sprintf("\n\n⚠️ Мем опубликован, но не удален из S3: %v", deleteErr)
			} else {
				b.log.Infof("handlePublish: meme successfully deleted from S3: %s", memeID)
				deleteStatus = "\n\n✅ Мем также удален из S3"

				// Delete other memes from the same batch (if any)
				b.deleteOtherBatchMemes(context.Background(), chatID, memeID)
			}

			finalMsg = fmt.Sprintf("📤 Результаты публикации:\n\n%s%s", strings.Join(resultLines, "\n"), deleteStatus)
			b.editMessageHTML(chatID, statusMsgID, finalMsg)
		} else {
			finalMsg = fmt.Sprintf("❌ Ошибка публикации:\n\n%s", strings.Join(resultLines, "\n"))
			b.editMessageHTML(chatID, statusMsgID, finalMsg)
			b.log.Errorf("handlePublish: FAILED - all platforms failed")
		}
	}()
}

// deleteOtherBatchMemes deletes all other memes from the same batch (slider) except the published one
// This is called after a meme is successfully published, assuming the user didn't like the others
func (b *TelegramBot) deleteOtherBatchMemes(ctx context.Context, chatID int64, publishedMemeID string) {
	b.sliderMux.Lock()
	batchMemes, ok := b.sliderMemes[chatID]
	b.sliderMux.Unlock()

	if !ok || len(batchMemes) == 0 {
		b.log.Infof("deleteOtherBatchMemes: no cached batch memes for chatID=%d", chatID)
		return
	}

	b.log.Infof("deleteOtherBatchMemes: START - chatID=%d, publishedMemeID=%s, batchSize=%d",
		chatID, publishedMemeID, len(batchMemes))

	// Collect IDs of other memes in the batch
	var otherIDs []string
	for _, m := range batchMemes {
		if m.ID != publishedMemeID {
			otherIDs = append(otherIDs, m.ID)
		}
	}

	b.sliderMux.Lock()
	delete(b.sliderMemes, chatID)
	delete(b.sliderMemesTTL, chatID)
	b.sliderMux.Unlock()

	if len(otherIDs) == 0 {
		b.log.Infof("deleteOtherBatchMemes: no other memes in batch to delete")
		return
	}

	// Batch delete: single S3 read+write instead of N reads+writes
	go func() {
		deleteCtx := context.Background()
		if err := b.svc.Impl().DeleteMemes(deleteCtx, otherIDs); err != nil {
			b.log.Errorf("deleteOtherBatchMemes: batch delete failed: %v", err)
		} else {
			b.log.Infof("deleteOtherBatchMemes: COMPLETE - batch deleted %d memes", len(otherIDs))
		}
	}()
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

func (b *TelegramBot) handleSelectMeme(ctx context.Context, chatID int64, memeID string, msgID int) {
	b.log.Infof("handleSelectMeme: memeID=%s, chatID=%d", memeID, chatID)

	// Find meme in cached slider memes
	memes, ok := b.sliderMemes[chatID]
	if !ok || len(memes) == 0 {
		b.replyText(chatID, "❌ Кэш мемов истёк. Запросите слайдер заново (/meme 3)")
		return
	}

	var selectedMeme *model.Meme
	for _, m := range memes {
		if m.ID == memeID {
			selectedMeme = m
			break
		}
	}

	if selectedMeme == nil {
		b.replyText(chatID, "❌ Мем не найден в списке")
		return
	}

	b.log.Infof("found selected meme: %s", selectedMeme.ID)
	// Send the selected meme with action buttons
	b.sendMemeVideo(ctx, chatID, selectedMeme)
}

func (b *TelegramBot) handleChangeAudio(ctx context.Context, chatID int64, memeID string, msgID int) {
	b.log.Infof("handleChangeAudio: START - memeID=%s, chatID=%d, msgID=%d", memeID, chatID, msgID)

	// Delete the callback message first (the one with the video and buttons)
	deleteMsg := tgbotapi.NewDeleteMessage(chatID, msgID)
	if _, err := b.tg.Send(deleteMsg); err != nil {
		b.log.Warnf("handleChangeAudio: failed to delete message %d: %v", msgID, err)
	}

	// Send status message
	b.replyText(chatID, "🎵 Замена трека...")

	// Start replacement in background
	go func() {
		b.log.Infof("handleChangeAudio: goroutine START - memeID=%s", memeID)

		// Replace the audio
		replacedMeme, err := b.svc.Impl().ReplaceAudioInMeme(ctx, memeID)
		if err != nil {
			b.log.Errorf("handleChangeAudio: failed to replace audio - memeID=%s, err=%v", memeID, err)
			b.replyText(chatID, fmt.Sprintf("❌ Ошибка замены трека: %v", err))
			return
		}

		b.log.Infof("handleChangeAudio: audio replaced successfully, new title=%s", replacedMeme.Title)

		// Brief delay for S3 sync
		time.Sleep(2 * time.Second)

		// Send the updated meme video with buttons
		b.sendMemeVideo(ctx, chatID, replacedMeme)
	}()
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

// handleDislike deletes the disliked meme and sends a new one
func (b *TelegramBot) handleDislike(ctx context.Context, chatID int64, memeID string, msgID int) {
	b.log.Infof("handleDislike: START - memeID=%s, chatID=%d", memeID, chatID)
	b.replyText(chatID, "👎 Удаляю этот мем и ищу новый...")

	go func() {
		deleteCtx := context.Background()

		// Delete the disliked meme
		if err := b.svc.Impl().DeleteMeme(deleteCtx, memeID); err != nil {
			b.log.Errorf("handleDislike: failed to delete meme %s: %v", memeID, err)
			b.replyText(chatID, fmt.Sprintf("❌ Ошибка удаления: %v", err))
			return
		}

		b.log.Infof("handleDislike: meme deleted successfully: %s", memeID)

		// Get a new random meme
		newMeme, err := b.svc.Impl().GetRandomMeme(deleteCtx)
		if err != nil {
			b.log.Errorf("handleDislike: failed to get new meme: %v", err)
			b.replyText(chatID, fmt.Sprintf("❌ Ошибка получения нового мема: %v", err))
			return
		}

		b.log.Infof("handleDislike: got new meme %s, sending to chat", newMeme.ID)

		// Brief delay for S3 sync
		time.Sleep(1 * time.Second)

		// Send the new meme
		b.sendMemeVideo(deleteCtx, chatID, newMeme)
	}()
}

// handleDislikeSlider deletes all memes in the slider and sends a new batch
func (b *TelegramBot) handleDislikeSlider(ctx context.Context, chatID int64, msgID int) {
	b.log.Infof("handleDislikeSlider: START - chatID=%d", chatID)
	b.replyText(chatID, "👎 Удаляю слайдер мемов и ищу новый набор...")

	go func() {
		deleteCtx := context.Background()

		// Get the cached memes for this slider
		memes, ok := b.sliderMemes[chatID]
		if !ok || len(memes) == 0 {
			b.log.Warnf("handleDislikeSlider: no cached memes found for chatID=%d", chatID)
			b.replyText(chatID, "❌ Кэш мемов слайдера не найден")
			return
		}

		b.log.Infof("handleDislikeSlider: deleting %d memes from slider...", len(memes))

		// Delete all memes in the slider
		for _, meme := range memes {
			if err := b.svc.Impl().DeleteMeme(deleteCtx, meme.ID); err != nil {
				b.log.Errorf("handleDislikeSlider: failed to delete meme %s: %v", meme.ID, err)
				// Continue deleting others
				continue
			}
			b.log.Infof("handleDislikeSlider: meme deleted: %s", meme.ID)
		}

		// Clear the cache
		delete(b.sliderMemes, chatID)

		b.log.Infof("handleDislikeSlider: all memes deleted from slider, fetching new batch...")

		// Get new batch of memes (same count as before)
		count := len(memes)
		newMemes, err := b.svc.Impl().GetRandomMemes(deleteCtx, count)
		if err != nil {
			b.log.Errorf("handleDislikeSlider: failed to get new memes: %v", err)
			b.replyText(chatID, fmt.Sprintf("❌ Ошибка получения новых мемов: %v", err))
			return
		}

		if len(newMemes) == 0 {
			b.log.Errorf("handleDislikeSlider: no new memes available")
			b.replyText(chatID, "❌ Нет доступных мемов")
			return
		}

		b.log.Infof("handleDislikeSlider: got %d new memes, sending slider...", len(newMemes))

		// Brief delay for S3 sync
		time.Sleep(1 * time.Second)

		// Send the new batch (using same logic as handleMultipleMemes)
		b.handleMultipleMemesWithMemes(deleteCtx, chatID, newMemes)
	}()
}

// Helper function to send multiple memes directly (without fetching new ones)
func (b *TelegramBot) handleMultipleMemesWithMemes(ctx context.Context, chatID int64, memes []*model.Meme) {
	if len(memes) == 0 {
		b.log.Errorf("handleMultipleMemesWithMemes: no memes provided")
		b.replyText(chatID, "❌ Нет доступных мемов")
		return
	}

	// Cache memes for this chat
	b.sliderMux.Lock()
	b.sliderMemes[chatID] = memes
	b.sliderMemesTTL[chatID] = time.Now().Add(30 * time.Minute)
	b.sliderMux.Unlock()

	b.log.Infof("handleMultipleMemesWithMemes: sending %d memes as media group to chat", len(memes))

	// Download all memes first
	videos := make([]string, 0, len(memes))
	for _, meme := range memes {
		videoPath, err := b.svc.Impl().DownloadMemeToTemp(ctx, meme)
		if err != nil {
			b.log.Errorf("handleMultipleMemesWithMemes: download meme %s: %v", meme.ID, err)
			continue
		}
		videos = append(videos, videoPath)
	}

	if len(videos) == 0 {
		b.log.Errorf("handleMultipleMemesWithMemes: failed to download any memes")
		b.replyText(chatID, "❌ Не удалось загрузить мемы")
		return
	}

	defer func() {
		for _, v := range videos {
			os.Remove(v)
		}
	}()

	// Build media group (up to 10 videos as slider)
	mediaGroup := make([]interface{}, 0, len(videos))
	for idx, videoPath := range videos {
		meme := memes[idx]

		f, err := os.Open(videoPath)
		if err != nil {
			b.log.Errorf("handleMultipleMemesWithMemes: open meme file %d: %v", idx+1, err)
			continue
		}
		defer f.Close()

		// Create caption with slider counter and title
		caption := fmt.Sprintf("%d/%d — %s", idx+1, len(videos), meme.Title)

		video := tgbotapi.NewInputMediaVideo(tgbotapi.FileReader{
			Name:   fmt.Sprintf("meme_%d.mp4", idx+1),
			Reader: f,
		})
		video.Caption = caption
		mediaGroup = append(mediaGroup, video)
	}

	if len(mediaGroup) > 0 {
		msg := tgbotapi.NewMediaGroup(chatID, mediaGroup)
		if _, err := b.tg.SendMediaGroup(msg); err != nil {
			b.log.Errorf("handleMultipleMemesWithMemes: send media group: %v", err)
			b.replyText(chatID, "❌ Ошибка отправки видео")
			return
		}
		b.log.Infof("✓ handleMultipleMemesWithMemes: sent %d memes as media group/slider", len(mediaGroup))
	}

	// Send selection buttons
	b.sendMemeSelectionButtons(chatID, memes)
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

func (b *TelegramBot) handleMeme(ctx context.Context, chatID int64, args string) {
	// Parse count from arguments
	count := 1
	if args != "" {
		_, err := fmt.Sscanf(strings.TrimSpace(args), "%d", &count)
		if err != nil || count < 1 {
			count = 1
		}
		if count > 10 {
			count = 10 // Limit to 10 memes per request
		}
	}

	if count == 1 {
		// Single meme without slider
		b.handleSingleMeme(ctx, chatID)
	} else {
		// Multiple memes as media group (slider)
		b.handleMultipleMemes(ctx, chatID, count)
	}
}

// handleSingleMeme sends a single meme without slider
func (b *TelegramBot) handleSingleMeme(ctx context.Context, chatID int64) {
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
	b.sendMemeVideo(ctx, chatID, meme)
}

// handleMultipleMemes sends N memes as a media group (slider)
func (b *TelegramBot) handleMultipleMemes(ctx context.Context, chatID int64, count int) {
	b.replyText(chatID, fmt.Sprintf("▶️ Загружаю %d мемов...", count))

	// Get N unique memes
	memes, err := b.svc.Impl().GetRandomMemes(ctx, count)
	if err != nil {
		b.log.Errorf("get random memes: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка получения мемов: %v", err))
		return
	}

	if len(memes) == 0 {
		b.log.Errorf("no memes available")
		b.replyText(chatID, "❌ Нет доступных мемов")
		return
	}

	// Cache memes for this chat
	b.sliderMux.Lock()
	b.sliderMemes[chatID] = memes
	b.sliderMemesTTL[chatID] = time.Now().Add(30 * time.Minute)
	b.sliderMux.Unlock()

	b.log.Infof("sending %d memes as media group to chat", len(memes))

	// Download all memes first
	videos := make([]string, 0, len(memes))
	for _, meme := range memes {
		videoPath, err := b.svc.Impl().DownloadMemeToTemp(ctx, meme)
		if err != nil {
			b.log.Errorf("download meme %s: %v", meme.ID, err)
			continue
		}
		videos = append(videos, videoPath)
	}

	if len(videos) == 0 {
		b.log.Errorf("failed to download any memes")
		b.replyText(chatID, "❌ Не удалось загрузить мемы")
		return
	}

	defer func() {
		for _, v := range videos {
			os.Remove(v)
		}
	}()

	// Build media group (up to 10 videos as slider)
	mediaGroup := make([]interface{}, 0, len(videos))
	for idx, videoPath := range videos {
		meme := memes[idx]

		f, err := os.Open(videoPath)
		if err != nil {
			b.log.Errorf("open meme file %d: %v", idx+1, err)
			continue
		}
		defer f.Close()

		// Create caption with slider counter and title
		caption := fmt.Sprintf("%d/%d — %s", idx+1, len(videos), meme.Title)

		video := tgbotapi.NewInputMediaVideo(tgbotapi.FileReader{
			Name:   fmt.Sprintf("meme_%d.mp4", idx+1),
			Reader: f,
		})
		video.Caption = caption
		mediaGroup = append(mediaGroup, video)
	}

	if len(mediaGroup) > 0 {
		msg := tgbotapi.NewMediaGroup(chatID, mediaGroup)
		if _, err := b.tg.SendMediaGroup(msg); err != nil {
			b.log.Errorf("send media group: %v", err)
			b.replyText(chatID, "❌ Ошибка отправки видео")
			return
		}
		b.log.Infof("✓ sent %d memes as media group/slider", len(mediaGroup))
	}

	// Send selection buttons
	b.sendMemeSelectionButtons(chatID, memes)
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
			tgbotapi.NewInlineKeyboardButtonData("👎 Не нравится", fmt.Sprintf("dislike:%s", meme.ID)),
		),
	)
	msg.ReplyMarkup = keyboard

	sentMsg, err := b.tg.Send(msg)
	if err != nil {
		b.log.Errorf("send meme: %v", err)
		b.replyText(chatID, "Ошибка отправки видео")
		return false
	}
	if sentMsg.Video == nil {
		b.log.Errorf("send meme: sentMsg.Video is nil")
		b.replyText(chatID, "Ошибка отправки видео")
		return false
	}
	return true
}

// sendMemeSelectionButtons sends buttons for selecting specific memes from slider
func (b *TelegramBot) sendMemeSelectionButtons(chatID int64, memes []*model.Meme) {
	if len(memes) == 0 {
		return
	}

	// Create rows with meme selection buttons (3 per row max)
	rows := make([][]tgbotapi.InlineKeyboardButton, 0)
	row := make([]tgbotapi.InlineKeyboardButton, 0)

	for idx, meme := range memes {
		// Truncate title to fit button
		title := meme.Title
		if len(title) > 20 {
			title = title[:17] + "..."
		}

		btn := tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("#%d: %s", idx+1, title),
			fmt.Sprintf("selectmeme:%s", meme.ID),
		)
		row = append(row, btn)

		// Start new row after 3 buttons
		if len(row) == 3 {
			rows = append(rows, row)
			row = make([]tgbotapi.InlineKeyboardButton, 0)
		}
	}

	// Add remaining buttons
	if len(row) > 0 {
		rows = append(rows, row)
	}

	// Add dislike button for the entire slider on a new row
	rows = append(rows, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData("👎 Не нравится слайдер", fmt.Sprintf("dislikeslider:%d", chatID)),
	})

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	msg := tgbotapi.NewMessage(chatID, "🎬 Выберите мем для работы:")
	msg.ReplyMarkup = keyboard
	b.tg.Send(msg)
}

func tempFilePath(prefix, name string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_").Replace(name)
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%s", prefix, safe))
}

// runSchedulePoster runs in background and sends memes at scheduled times
func (b *TelegramBot) runSchedulePoster(ctx context.Context) {
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

			// Always get fresh schedule from service (in case it was updated via /setnext)
			currentSched := b.svc.GetSchedule()
			if currentSched == nil {
				b.log.Errorf("schedule is nil, skipping check")
				continue
			}

			// Reload schedule if it's a new day
			if currentSched.Date != now.Format("2006-01-02") {
				newSched, err := scheduler.GetOrCreateSchedule(ctx, b.svc.GetS3Client(), &cfg, now)
				if err == nil && newSched != nil {
					currentSched = newSched
					b.svc.SetSchedule(currentSched)
					sentTimes = make(map[string]bool) // Reset sent times
					b.log.Infof("reloaded schedule for %s with %d entries", currentSched.Date, len(currentSched.Entries))
				}
			}

			// Check each entry in schedule
			for _, entry := range currentSched.Entries {
				timeKey := entry.Time.Format("15:04:05")

				// Skip if already sent
				if sentTimes[timeKey] {
					continue
				}

				// Check if it's time to send (within 1 minute window)
				timeDiff := now.Sub(entry.Time)
				if timeDiff >= 0 && timeDiff < 1*time.Minute {
					b.log.Infof("runSchedulePoster: sending 3 memes at scheduled time %s (now=%s, diff=%v)",
						entry.Time.Format("15:04:05"), now.Format("15:04:05"), timeDiff)
					// Use background context for scheduled sends to avoid cancellation
					go b.sendScheduledMemes(context.Background(), chatID)
					sentTimes[timeKey] = true
				}
			}

			// Clean up expired cached files (runs every 10 seconds)
			go b.clearExpiredMemeCache()
		}
	}
}

// getCachedOrDownloadMeme retrieves cached meme file or downloads from S3 with caching
// Optimization: avoids re-downloading same meme multiple times within TTL (default 5 minutes)
// This significantly reduces S3 egress traffic during scheduled posting
func (b *TelegramBot) getCachedOrDownloadMeme(ctx context.Context, meme *model.Meme) (string, error) {
	// Check if already cached and not expired
	b.memeCacheMux.RLock()
	cachedPath, exists := b.memeFileCache[meme.ID]
	expireTime, hasExpire := b.memeCacheTTL[meme.ID]
	b.memeCacheMux.RUnlock()

	now := time.Now()
	if exists && hasExpire && now.Before(expireTime) {
		// Cache hit and not expired
		if stat, err := os.Stat(cachedPath); err == nil && stat.Size() > 0 {
			b.log.Infof("getCachedOrDownloadMeme: cache HIT for meme %s (expires in %v)", meme.ID, expireTime.Sub(now))
			return cachedPath, nil
		}
		// File was deleted, remove from cache
		b.memeCacheMux.Lock()
		delete(b.memeFileCache, meme.ID)
		delete(b.memeCacheTTL, meme.ID)
		b.memeCacheMux.Unlock()
	}

	// Cache miss or expired - download from S3
	b.log.Infof("getCachedOrDownloadMeme: cache MISS for meme %s, downloading from S3...", meme.ID)
	videoPath, err := b.svc.Impl().DownloadMemeToTemp(ctx, meme)
	if err != nil {
		b.log.Errorf("getCachedOrDownloadMeme: download failed: %v", err)
		return "", err
	}

	// Store in cache with TTL
	b.memeCacheMux.Lock()
	b.memeFileCache[meme.ID] = videoPath
	b.memeCacheTTL[meme.ID] = now.Add(b.memeCacheTTLDuration)
	b.memeCacheMux.Unlock()

	b.log.Infof("getCachedOrDownloadMeme: cached meme %s (TTL: %v)", meme.ID, b.memeCacheTTLDuration)
	return videoPath, nil
}

// clearExpiredMemeCache removes cached files that have expired
// Should be called periodically to free disk space
// Runs every 1 minute to clean up old files
func (b *TelegramBot) clearExpiredMemeCache() {
	b.memeCacheMux.Lock()
	defer b.memeCacheMux.Unlock()

	now := time.Now()
	expiredCount := 0

	for memeID, expireTime := range b.memeCacheTTL {
		if now.After(expireTime) {
			// Remove expired file
			if filePath, ok := b.memeFileCache[memeID]; ok {
				if err := os.Remove(filePath); err != nil {
					b.log.Errorf("clearExpiredMemeCache: failed to remove file %s: %v", filePath, err)
				}
				delete(b.memeFileCache, memeID)
				expiredCount++
			}
			delete(b.memeCacheTTL, memeID)
		}
	}

	if expiredCount > 0 {
		b.log.Infof("clearExpiredMemeCache: removed %d expired cached files (freeing disk space)", expiredCount)
	}
}

// runMaintenanceTicker runs a periodic background task every 2 minutes that cleans
// up all in-memory caches with TTL: meme file cache, slider memes, search state.
func (b *TelegramBot) runMaintenanceTicker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.clearExpiredMemeCache()
			b.clearExpiredSliderMemes()
			b.clearExpiredSearchState()
		}
	}
}

// clearExpiredSliderMemes evicts slider meme entries whose TTL has expired.
// Prevents unbounded growth of sliderMemes map when users never publish.
func (b *TelegramBot) clearExpiredSliderMemes() {
	b.sliderMux.Lock()
	defer b.sliderMux.Unlock()
	now := time.Now()
	evicted := 0
	for chatID, exp := range b.sliderMemesTTL {
		if now.After(exp) {
			delete(b.sliderMemes, chatID)
			delete(b.sliderMemesTTL, chatID)
			evicted++
		}
	}
	if evicted > 0 {
		b.log.Infof("clearExpiredSliderMemes: evicted %d stale entries", evicted)
	}
}

// clearExpiredSearchState removes trackSearchState/trackSearchMode entries that
// were set more than 5 minutes ago (user never replied).
func (b *TelegramBot) clearExpiredSearchState() {
	// Clean trackSearchTimestamp / trackSearchState first
	b.trackSearchMux.Lock()
	now := time.Now()
	var staleIDs []int64
	for chatID, ts := range b.trackSearchTimestamp {
		if now.Sub(ts) > 5*time.Minute {
			staleIDs = append(staleIDs, chatID)
		}
	}
	for _, chatID := range staleIDs {
		delete(b.trackSearchState, chatID)
		delete(b.trackSearchTimestamp, chatID)
	}
	b.trackSearchMux.Unlock()

	if len(staleIDs) == 0 {
		return
	}

	// Clean corresponding trackSearchMode entries
	b.trackSearchModeMux.Lock()
	for _, chatID := range staleIDs {
		delete(b.trackSearchMode, chatID)
	}
	b.trackSearchModeMux.Unlock()

	b.log.Infof("clearExpiredSearchState: evicted %d stale search entries", len(staleIDs))
}

// sendScheduledMemes sends 3 unique memes as media group (slider) to the scheduled chat
// Uses the same logic as /meme command: sends media group + selection buttons
func (b *TelegramBot) sendScheduledMemes(ctx context.Context, chatID int64) {
	b.log.Infof("sendScheduledMemes: START - chatID=%d", chatID)

	// Get 3 unique memes
	memes, err := b.svc.Impl().GetRandomMemes(ctx, 3)
	if err != nil {
		b.log.Errorf("sendScheduledMemes: get random memes failed: %v", err)
		return
	}

	if len(memes) == 0 {
		b.log.Errorf("sendScheduledMemes: no memes available")
		return
	}

	b.log.Infof("sendScheduledMemes: got %d memes, downloading videos with cache...", len(memes))

	// Download all memes first (use cached version if available and not expired)
	videos := make([]string, 0, len(memes))
	for _, meme := range memes {
		videoPath, err := b.getCachedOrDownloadMeme(ctx, meme)
		if err != nil {
			b.log.Errorf("sendScheduledMemes: cache/download meme %s failed: %v", meme.ID, err)
			continue
		}
		videos = append(videos, videoPath)
	}

	if len(videos) == 0 {
		b.log.Errorf("sendScheduledMemes: failed to download any memes")
		return
	}

	b.log.Infof("sendScheduledMemes: prepared %d videos, building media group...", len(videos))

	// Note: DO NOT defer cleanup - files are managed by meme cache with TTL
	// Cache cleanup happens via clearExpiredMemeCache() called periodically

	// Build media group (up to 3 videos as slider)
	mediaGroup := make([]interface{}, 0, len(videos))
	for idx, videoPath := range videos {
		meme := memes[idx]

		f, err := os.Open(videoPath)
		if err != nil {
			b.log.Errorf("sendScheduledMemes: open meme file %d failed: %v", idx+1, err)
			continue
		}
		defer f.Close()

		// Create caption with slider counter and title
		caption := fmt.Sprintf("%d/%d — %s", idx+1, len(videos), meme.Title)

		video := tgbotapi.NewInputMediaVideo(tgbotapi.FileReader{
			Name:   fmt.Sprintf("meme_%d.mp4", idx+1),
			Reader: f,
		})
		video.Caption = caption
		mediaGroup = append(mediaGroup, video)
	}

	if len(mediaGroup) == 0 {
		b.log.Errorf("sendScheduledMemes: media group is empty after building")
		return
	}

	b.log.Infof("sendScheduledMemes: sending %d videos as media group...", len(mediaGroup))
	msg := tgbotapi.NewMediaGroup(chatID, mediaGroup)
	if _, err := b.tg.SendMediaGroup(msg); err != nil {
		b.log.Errorf("sendScheduledMemes: SendMediaGroup failed: %v", err)
		return
	}
	b.log.Infof("sendScheduledMemes: ✓ successfully sent %d memes as media group", len(mediaGroup))

	// Cache the selected memes for selection buttons (using same cache as /meme command)
	b.log.Infof("sendScheduledMemes: caching %d memes for button callbacks...", len(memes))
	b.sliderMux.Lock()
	b.sliderMemes[chatID] = memes
	b.sliderMemesTTL[chatID] = time.Now().Add(30 * time.Minute)
	b.sliderMux.Unlock()

	// Send selection buttons (same as /meme command)
	b.log.Infof("sendScheduledMemes: sending selection buttons...")
	b.sendMemeSelectionButtons(chatID, memes)
	b.log.Infof("sendScheduledMemes: COMPLETE")
}

func (b *TelegramBot) cmdHelp(chatID int64) {
	help := `Команды:
/start — приветствие
/help — помощь
/meme [count] — получить мем(ы) из пула (count: 1-10, по умолчанию 1)
             /meme — один мем с кнопками действий
             /meme 3 — 3 мема слайдером (медиагруппой)
/idea [query] — получить идею для видео на основе песни и сгенерировать видео
              /idea — выбрать из списка, случайный или поиск
              /idea Dua Lipa — найти треки Dua Lipa и выбрать
/song [query] — скачать трек в формате MP3 или MP4A
              /song — выбрать из списка, случайный или поиск
              /song Dua Lipa — найти треки Dua Lipa и скачать
/status — статус генерации и использование памяти
/errors — скачать файл errors.log с последними ошибками
/chatid — показать текущий chat ID
/scheduleinfo — расписание отправок мемов на сегодня
/setnext <index> <time> — изменить время отправки по индексу
                   /setnext 1 14:30 (на 14:30)
                   /setnext 2 +30m (через 30 минут)
                   /setnext 3 +2h (через 2 часа)
                   /setnext 4 2025-01-28 14:30 (конкретная дата и время)
/runscheduled — запустить генерацию 3 мемов сейчас (с кнопками действий)
/clearschedule — удалить schedule.json и сгенерировать расписание заново
/clearsources — очистить папку источников
/clearmemes — очистить папку мемов и memes.json
/sync — синхронизировать sources.json и memes.json с S3
/forcecheck — принудительно проверить и восстановить ресурсы
/checkfiles — проверить наличие и размер файлов (token.pickle, client_secrets.json)
/uploadtoken — загрузить token.pickle как документ
/uploadclient — загрузить client_secrets.json как документ
/syncfiles — загрузить все файлы в S3
/downloadfiles — загрузить все файлы из S3 локально

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

// cmdSetNext updates the time of a scheduled entry
// Usage: /setnext <index> <HH:MM | +30m | +2h | YYYY-MM-DD HH:MM>
func (b *TelegramBot) cmdSetNext(ctx context.Context, chatID int64, args string) {
	parts := strings.Fields(args)
	if len(parts) < 2 {
		b.replyText(chatID, "Использование: /setnext <index> <HH:MM | +30m | +2h | YYYY-MM-DD HH:MM>")
		return
	}

	b.log.Infof("cmdSetNext: START - args=%v", parts)

	// Parse index
	var idx int
	_, err := fmt.Sscanf(parts[0], "%d", &idx)
	if err != nil {
		b.log.Errorf("cmdSetNext: invalid index: %v", err)
		b.replyText(chatID, "Первый параметр должен быть индексом (#) из /scheduleinfo")
		return
	}
	b.log.Infof("cmdSetNext: parsed index=%d", idx)

	// Get current schedule
	sched := b.svc.GetSchedule()
	if sched == nil {
		b.log.Errorf("cmdSetNext: schedule is nil")
		b.replyText(chatID, "❌ Расписание не загружено")
		return
	}
	b.log.Infof("cmdSetNext: got schedule with %d entries", len(sched.Entries))

	if idx < 1 || idx > len(sched.Entries) {
		b.log.Errorf("cmdSetNext: index out of range: %d (max=%d)", idx, len(sched.Entries))
		b.replyText(chatID, "❌ Неверный индекс")
		return
	}

	// Parse target time
	rawTime := strings.Join(parts[1:], " ")
	b.log.Infof("cmdSetNext: parsing time format: %q", rawTime)
	baseEntry := sched.Entries[idx-1]
	baseDt := baseEntry.Time
	b.log.Infof("cmdSetNext: current time for index=%d is %s", idx, baseDt.Format("15:04:05"))

	var targetTime time.Time

	// Try parsing as relative time: +30m, +2h, -1h, etc.
	if strings.HasPrefix(rawTime, "+") || strings.HasPrefix(rawTime, "-") {
		b.log.Infof("cmdSetNext: detected relative time format")
		sign := 1
		if strings.HasPrefix(rawTime, "-") {
			sign = -1
		}

		rawTime = strings.TrimPrefix(strings.TrimPrefix(rawTime, "+"), "-")
		// Extract number and unit
		var num int
		var unit rune
		_, scanErr := fmt.Sscanf(rawTime, "%d%c", &num, &unit)
		if scanErr != nil {
			b.log.Errorf("cmdSetNext: failed to parse relative time: %v", scanErr)
			b.replyText(chatID, "❌ Не удалось распарсить относительное время. Примеры: +30m, +2h, -1h")
			return
		}

		var delta time.Duration
		switch unit {
		case 'm':
			delta = time.Duration(sign*num) * time.Minute
		case 'h':
			delta = time.Duration(sign*num) * time.Hour
		case 'd':
			delta = time.Duration(sign*num) * 24 * time.Hour
		default:
			b.log.Errorf("cmdSetNext: unknown time unit: %c", unit)
			b.replyText(chatID, "❌ Неизвестная единица времени. Используйте: m (минуты), h (часы), d (дни)")
			return
		}

		targetTime = baseDt.Add(delta)
		b.log.Infof("cmdSetNext: calculated target time (relative): %s (delta=%v)", targetTime.Format("15:04:05"), delta)
	} else if strings.Contains(rawTime, ":") && !strings.Contains(rawTime, "-") {
		// Parse as HH:MM
		b.log.Infof("cmdSetNext: detected HH:MM format")
		parts := strings.Split(rawTime, ":")
		if len(parts) != 2 {
			b.log.Errorf("cmdSetNext: invalid HH:MM format")
			b.replyText(chatID, "❌ Неверный формат HH:MM")
			return
		}

		var hour, min int
		_, hErr := fmt.Sscanf(parts[0], "%d", &hour)
		_, mErr := fmt.Sscanf(parts[1], "%d", &min)
		if hErr != nil || mErr != nil {
			b.log.Errorf("cmdSetNext: failed to parse HH:MM: hErr=%v, mErr=%v", hErr, mErr)
			b.replyText(chatID, "❌ Неверный формат HH:MM")
			return
		}

		targetTime = baseDt.
			Add(-time.Duration(baseDt.Hour()) * time.Hour).
			Add(-time.Duration(baseDt.Minute()) * time.Minute).
			Add(-time.Duration(baseDt.Second()) * time.Second).
			Add(time.Duration(hour) * time.Hour).
			Add(time.Duration(min) * time.Minute)
		b.log.Infof("cmdSetNext: calculated target time (HH:MM): %s", targetTime.Format("15:04:05"))
	} else {
		// Try parsing as full datetime: YYYY-MM-DD HH:MM or YYYY-MM-DDTHH:MM
		b.log.Infof("cmdSetNext: detected full datetime format")
		rawTime = strings.ReplaceAll(rawTime, "T", " ")
		layout := "2006-01-02 15:04"
		parsedTime, parseErr := time.Parse(layout, rawTime)
		if parseErr != nil {
			b.log.Errorf("cmdSetNext: failed to parse datetime: %v", parseErr)
			b.replyText(chatID, "❌ Не удалось распарсить время. Примеры:\n• 14:30 (HH:MM)\n• +30m (относительное)\n• 2025-01-28 14:30 (полная дата)")
			return
		}
		targetTime = parsedTime
	}

	// Validate that target time is not in the past
	now := time.Now()
	if targetTime.Before(now) {
		b.log.Errorf("cmdSetNext: target time in past: %s (now=%s)", targetTime.Format("15:04:05"), now.Format("15:04:05"))
		b.replyText(chatID, "❌ Нельзя установить время в прошлом")
		return
	}
	b.log.Infof("cmdSetNext: target time validated, proceeding with update")

	// Update the schedule
	updatedEntries := make([]scheduler.ScheduleEntry, len(sched.Entries))
	for i, entry := range sched.Entries {
		if i == idx-1 {
			updatedEntries[i] = scheduler.ScheduleEntry{Time: targetTime}
			b.log.Infof("cmdSetNext: updated entry[%d]: %s → %s", i, entry.Time.Format("15:04:05"), targetTime.Format("15:04:05"))
		} else {
			updatedEntries[i] = entry
		}
	}

	updatedSched := &scheduler.DailySchedule{
		Date:      sched.Date,
		Entries:   updatedEntries,
		UpdatedAt: time.Now(),
	}
	b.log.Infof("cmdSetNext: created updated schedule object")

	// Save to S3
	cfg := b.svc.GetConfig()
	saveErr := scheduler.SaveSchedule(ctx, b.svc.GetS3Client(), &cfg, updatedSched)
	if saveErr != nil {
		b.log.Errorf("save schedule: %v", saveErr)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка сохранения расписания: %v", saveErr))
		return
	}

	// Update in-memory schedule
	b.svc.SetSchedule(updatedSched)

	b.log.Infof("cmdSetNext: schedule updated - index=%d, new time=%s", idx, targetTime.Format("15:04:05"))
	b.replyText(chatID, fmt.Sprintf("✅ Время обновлено на %s. /scheduleinfo для просмотра.", targetTime.Format("15:04:05")))
}

func (b *TelegramBot) cmdRunScheduled(ctx context.Context, chatID int64) {
	b.replyText(chatID, "▶️ Генерирую 3 мема прямо сейчас...")

	// Generate 3 memes (same logic as /meme 3 command)
	b.handleMultipleMemes(ctx, chatID, 3)
}

// cmdClearSchedule deletes schedule.json and regenerates it for today
func (b *TelegramBot) cmdClearSchedule(ctx context.Context, chatID int64) {
	b.replyText(chatID, "🗑️ Удаляю schedule.json...")

	// Delete from S3
	if err := b.svc.GetS3Client().Delete(ctx, "schedule.json"); err != nil {
		b.log.Warnf("delete schedule from S3: %v (might not exist)", err)
	}

	// Generate new schedule for today
	cfg := b.svc.GetConfig()
	now := time.Now()
	newSched, err := scheduler.GetOrCreateSchedule(ctx, b.svc.GetS3Client(), &cfg, now)
	if err != nil {
		b.log.Errorf("create new schedule: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка создания расписания: %v", err))
		return
	}

	if newSched == nil {
		b.replyText(chatID, "❌ Не удалось создать расписание")
		return
	}

	// Update in-memory schedule
	b.svc.SetSchedule(newSched)

	// Show new schedule
	b.log.Infof("new schedule generated for %s", newSched.Date)
	b.replyText(chatID, fmt.Sprintf("✅ Расписание пересоздано для %s. Используйте /scheduleinfo для просмотра.", newSched.Date))
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

func (b *TelegramBot) cmdClearMemes(ctx context.Context, chatID int64) {
	b.replyText(chatID, "🗑️ Очищаю папку мемов...")

	if err := b.svc.ClearMemes(ctx); err != nil {
		b.log.Errorf("clear memes: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка при очистке: %v", err))
		return
	}

	b.replyText(chatID, "✅ Папка мемов и memes.json успешно очищены")
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

func (b *TelegramBot) cmdCheckFiles(chatID int64) {
	files := map[string]string{
		"token.pickle":          "token.pickle",
		"token_eenfinit.pickle": "token_eenfinit.pickle",
		"client_secrets.json":   "client_secrets.json",
	}

	lines := []string{"Проверка обязательных файлов:"}

	ctx := context.Background()
	s3Client := b.svc.GetS3Client()

	for label, path := range files {
		// Check local file
		stat, err := os.Stat(path)
		var status string

		if err != nil && os.IsNotExist(err) {
			status = "❌ отсутствует"
		} else if err != nil {
			status = fmt.Sprintf("⚠️ ошибка проверки (%v)", err)
		} else if stat.IsDir() {
			status = "⚠️ это директория (ожидается файл)"
		} else if stat.Size() == 0 {
			status = "⚠️ пустой файл"
		} else {
			status = fmt.Sprintf("✅ найден (%d байт)", stat.Size())
		}

		// Check S3 file
		s3Key := fmt.Sprintf("%s/%s", b.s3BucketDir, label)
		_, _, s3Err := s3Client.GetBytes(ctx, s3Key)
		var s3Status string

		if s3Err == nil {
			s3Status = "✅ в S3"
		} else {
			s3Status = "❌ нет в S3"
		}

		lines = append(lines, fmt.Sprintf("• %s: %s | %s", label, status, s3Status))
	}

	lines = append(lines, "")
	lines = append(lines, "Загрузка файлов:")
	lines = append(lines, "/uploadtoken — загрузить token.pickle (YouTube)")
	lines = append(lines, "/uploadclient — загрузить client_secrets.json (YouTube)")

	b.replyText(chatID, strings.Join(lines, "\n"))
}

func (b *TelegramBot) cmdUploadToken(chatID int64) {
	b.replyText(chatID, "📎 Пришлите файл token.pickle как документ следующим сообщением")
}

func (b *TelegramBot) cmdUploadClient(chatID int64) {
	b.replyText(chatID, "📎 Пришлите файл client_secrets.json как документ следующим сообщением")
}

func (b *TelegramBot) handleDocument(ctx context.Context, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	doc := msg.Document
	if doc == nil {
		return
	}

	fileName := strings.ToLower(doc.FileName)
	var targetPath string
	var s3Key string

	switch {
	case fileName == "token.pickle" || strings.HasSuffix(fileName, "/token.pickle"):
		targetPath = "token.pickle"
		s3Key = fmt.Sprintf("%s/token.pickle", b.s3BucketDir)
	case fileName == "token_eenfinit.pickle" || strings.HasSuffix(fileName, "/token_eenfinit.pickle"):
		targetPath = "token_eenfinit.pickle"
		s3Key = fmt.Sprintf("%s/token_eenfinit.pickle", b.s3BucketDir)
	case fileName == "client_secrets.json" || strings.HasSuffix(fileName, "/client_secrets.json"):
		targetPath = "client_secrets.json"
		s3Key = fmt.Sprintf("%s/client_secrets.json", b.s3BucketDir)
	default:
		b.replyText(chatID, "❌ Неизвестный файл. Ожидаю: token.pickle, token_eenfinit.pickle или client_secrets.json")
		return
	}

	b.log.Infof("uploading file: %s to local:%s and S3:%s", doc.FileName, targetPath, s3Key)

	// Download file from Telegram
	file, err := b.tg.GetFile(tgbotapi.FileConfig{FileID: doc.FileID})
	if err != nil {
		b.log.Errorf("failed to get file: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка загрузки: %v", err))
		return
	}

	// Download file content
	downloadURL := file.Link(os.Getenv("TELEGRAM_BOT_TOKEN"))
	resp, err := b.downloadFile(ctx, downloadURL)
	if err != nil {
		b.log.Errorf("failed to download file content: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка скачивания: %v", err))
		return
	}
	defer resp.Close()

	// Read all content into memory
	fileContent, err := io.ReadAll(resp)
	if err != nil {
		b.log.Errorf("failed to read file content: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка чтения файла: %v", err))
		return
	}

	// Save to local file
	if err := b.saveFile(targetPath, bytes.NewReader(fileContent)); err != nil {
		b.log.Errorf("failed to save local file: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка сохранения локального файла: %v", err))
		return
	}
	b.log.Infof("saved local file: %s (%d bytes)", targetPath, len(fileContent))

	// Save to S3
	s3Client := b.svc.GetS3Client()
	if err := s3Client.PutBytes(ctx, s3Key, fileContent, "application/octet-stream"); err != nil {
		b.log.Errorf("failed to save to S3: %v", err)
		b.replyText(chatID, fmt.Sprintf("⚠️ Локальный файл сохранён, но ошибка S3: %v", err))
		return
	}
	b.log.Infof("saved to S3: %s (%d bytes)", s3Key, len(fileContent))

	b.replyText(chatID, fmt.Sprintf("✅ Файл сохранён:\n• Локально: %s\n• S3: %s", targetPath, s3Key))
}

func (b *TelegramBot) downloadFile(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("unexpected status code: %d", response.StatusCode)
	}

	return response.Body, nil
}

func (b *TelegramBot) saveFile(path string, reader io.Reader) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, reader)
	return err
}

func (b *TelegramBot) cmdSyncFiles(ctx context.Context, chatID int64) {
	b.replyText(chatID, "📤 Загружаю файлы в S3...")

	files := map[string]string{
		"token.pickle":          "token.pickle",
		"token_eenfinit.pickle": "token_eenfinit.pickle",
		"client_secrets.json":   "client_secrets.json",
	}

	s3Client := b.svc.GetS3Client()
	uploadedCount := 0
	failedCount := 0
	missingCount := 0

	for label, path := range files {
		// Check if file exists locally
		fileContent, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				b.log.Warnf("file not found locally: %s", path)
				missingCount++
				continue
			}
			b.log.Errorf("failed to read file %s: %v", path, err)
			failedCount++
			continue
		}

		// Upload to S3
		s3Key := fmt.Sprintf("%s/%s", b.s3BucketDir, label)
		if err := s3Client.PutBytes(ctx, s3Key, fileContent, "application/octet-stream"); err != nil {
			b.log.Errorf("failed to upload to S3: %s - %v", s3Key, err)
			failedCount++
			continue
		}

		b.log.Infof("uploaded to S3: %s (%d bytes)", s3Key, len(fileContent))
		uploadedCount++
	}

	statusMsg := fmt.Sprintf("✅ Результат синхронизации:\n• Загружено: %d\n• Ошибок: %d\n• Отсутствует: %d",
		uploadedCount, failedCount, missingCount)
	b.replyText(chatID, statusMsg)
}

func (b *TelegramBot) cmdDownloadFiles(ctx context.Context, chatID int64) {
	b.replyText(chatID, "📥 Загружаю файлы из S3...")

	files := []string{
		"token.pickle",
		"token_eenfinit.pickle",
		"client_secrets.json",
	}

	s3Client := b.svc.GetS3Client()
	downloadedCount := 0
	failedCount := 0
	missingCount := 0

	for _, fileName := range files {
		s3Key := fmt.Sprintf("%s/%s", b.s3BucketDir, fileName)

		// Download from S3
		fileContent, _, err := s3Client.GetBytes(ctx, s3Key)
		if err != nil {
			b.log.Warnf("file not found in S3: %s", s3Key)
			missingCount++
			continue
		}

		// Save locally
		if err := os.WriteFile(fileName, fileContent, 0644); err != nil {
			b.log.Errorf("failed to save file locally: %s - %v", fileName, err)
			failedCount++
			continue
		}

		b.log.Infof("downloaded from S3 and saved: %s (%d bytes)", fileName, len(fileContent))
		downloadedCount++
	}

	statusMsg := fmt.Sprintf("✅ Результат загрузки:\n• Загружено: %d\n• Ошибок: %d\n• Отсутствует в S3: %d",
		downloadedCount, failedCount, missingCount)
	b.replyText(chatID, statusMsg)
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

func (b *TelegramBot) cmdIdea(ctx context.Context, chatID int64, args string) {
	b.log.Infof("cmdIdea: START - args=%s", args)

	args = strings.TrimSpace(args)

	// If user provided search query
	if args != "" {
		b.handleIdeaSearch(ctx, chatID, args)
		return
	}

	// Get songs from storage
	songs, err := b.svc.GetAllSongs(ctx)
	if err != nil || len(songs) == 0 {
		b.replyText(chatID, "❌ Ошибка: нет доступных песен")
		return
	}

	b.log.Infof("cmdIdea: got %d songs", len(songs))

	// Create inline keyboard with options
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🎲 Случайный трек", "ideagen:random"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Выбрать трек из списка", "idealist:0"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔍 Поиск трека", "ideasearch"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, "🎬 Генератор идей для видео\n\nВыберите трек, возьмите случайный или найдите по названию:")
	msg.ReplyMarkup = keyboard
	b.tg.Send(msg)
}

func (b *TelegramBot) handleIdeaGeneration(ctx context.Context, chatID int64, songID string) {
	b.log.Infof("handleIdeaGeneration: START - songID=%s", songID)

	// Get the song (random or by ID)
	var song *model.Song
	var err error

	if songID == "random" {
		song, err = b.svc.GetRandomSong(ctx)
	} else {
		song, err = b.svc.GetSongByID(ctx, songID)
	}

	if err != nil {
		b.log.Errorf("handleIdeaGeneration: get song failed: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка: песня не найдена - %v", err))
		return
	}

	b.log.Infof("handleIdeaGeneration: processing song: %s (%s)", song.Title, song.Author)

	// Show processing message
	procMsgID := b.replyText(chatID, fmt.Sprintf("⏳ Анализирую трек: %s - %s\n🎨 Генерирую видеоконцепцию и промпт...", song.Author, song.Title))

	// Generate ideas using AI
	titleGenerator := b.svc.GetTitleGenerator()
	if titleGenerator == nil {
		b.log.Errorf("handleIdeaGeneration: title generator not available")
		b.editMessage(chatID, procMsgID, "❌ Ошибка: генератор идей не инициализирован")
		return
	}

	ideas, err := titleGenerator.GenerateIdeaForSong(ctx, song)
	if err != nil {
		b.log.Errorf("handleIdeaGeneration: generate ideas failed: %v", err)
		b.editMessage(chatID, procMsgID, fmt.Sprintf("❌ Ошибка при генерации идеи: %v", err))
		return
	}

	b.log.Infof("handleIdeaGeneration: idea generated with %d scenes", len(ideas))

	cfg := b.svc.GetConfig()

	// Build result text for file and message
	scenesText := ""
	for _, scene := range ideas {
		scenesText += scene + "\n\n"
	}
	downloadURL := fmt.Sprintf("%s/%s/%s",
		strings.TrimRight(cfg.S3Endpoint, "/"),
		cfg.S3Bucket,
		song.AudioKey,
	)

	// First message: Track info and download link
	trackInfoMsg := fmt.Sprintf(
		"🎵 <b>Трек:</b> %s\n"+
			"👤 <b>Артист:</b> %s\n"+
			"⏱️ <b>Длительность:</b> %.1f сек\n"+
			"🔗 <a href=\"%s\">Скачать трек</a>",
		song.Title,
		song.Author,
		song.DurationS,
		downloadURL,
	)

	if err := b.editMessageHTML(chatID, procMsgID, trackInfoMsg); err != nil {
		b.log.Errorf("handleIdeaGeneration: failed to edit message: %v", err)
		b.replyHTML(chatID, trackInfoMsg)
	}

	// Extract [ПРОМПТ] section for separate Telegram message
	var aiPromptText string
	for _, idea := range ideas {
		if strings.HasPrefix(idea, "[ПРОМПТ]") {
			aiPromptText = strings.TrimSpace(strings.TrimPrefix(idea, "[ПРОМПТ]"))
			break
		}
	}

	videoPrompt := strings.TrimSpace(aiPromptText)
	if videoPrompt == "" {
		videoPrompt = strings.TrimSpace(scenesText)
	}
	if videoPrompt == "" {
		videoPrompt = fmt.Sprintf("Create a cinematic video inspired by %s by %s.", song.Title, song.Author)
	}

	var generatedVideoURL string
	var generatedOperationID string
	if cfg.BratuhaAPIKey != "" {
		b.replyText(chatID, "🎥 Запускаю генерацию видео через Bratuha Grok Video...")

		videoClient := ai.NewBratuhaVideoClient(cfg.BratuhaAPIKey, b.log)
		generatedVideoURL, generatedOperationID, err = videoClient.GenerateVideoURL(ctx, videoPrompt)
		if err != nil {
			b.log.Errorf("handleIdeaGeneration: video generation failed: %v", err)
			b.replyText(chatID, fmt.Sprintf("⚠️ Видео не удалось сгенерировать: %v", err))
		} else {
			b.log.Infof("handleIdeaGeneration: video generated via Bratuha operation=%s url=%s", generatedOperationID, generatedVideoURL)

			generatedVideoBody, err := b.downloadFile(ctx, generatedVideoURL)
			if err != nil {
				b.log.Errorf("handleIdeaGeneration: failed to download generated video: %v", err)
				b.replyText(chatID, fmt.Sprintf("⚠️ Видео готово, но не удалось скачать файл: %v", err))
			} else {
				defer generatedVideoBody.Close()

				generatedVideoTemp, err := os.CreateTemp("", fmt.Sprintf("idea_%s_*.mp4", song.ID))
				if err != nil {
					b.log.Errorf("handleIdeaGeneration: failed to create temp file for generated video: %v", err)
					b.replyText(chatID, "⚠️ Не удалось подготовить временный файл для видео")
				} else {
					generatedVideoPath := generatedVideoTemp.Name()
					if _, err := io.Copy(generatedVideoTemp, generatedVideoBody); err != nil {
						generatedVideoTemp.Close()
						os.Remove(generatedVideoPath)
						b.log.Errorf("handleIdeaGeneration: failed to save generated video: %v", err)
						b.replyText(chatID, "⚠️ Не удалось сохранить сгенерированное видео")
					} else {
						generatedVideoTemp.Close()

						songPath, err := b.svc.Impl().DownloadSongToTemp(ctx, song)
						if err != nil {
							os.Remove(generatedVideoPath)
							b.log.Errorf("handleIdeaGeneration: failed to download song for mux: %v", err)
							b.replyText(chatID, fmt.Sprintf("⚠️ Не удалось скачать трек для добавления музыки: %v", err))
						} else {
							defer os.Remove(songPath)

							finalVideoTemp, err := os.CreateTemp("", fmt.Sprintf("idea_%s_final_*.mp4", song.ID))
							if err != nil {
								os.Remove(generatedVideoPath)
								b.log.Errorf("handleIdeaGeneration: failed to create temp file for final video: %v", err)
								b.replyText(chatID, "⚠️ Не удалось подготовить итоговый видеофайл")
							} else {
								finalVideoPath := finalVideoTemp.Name()
								finalVideoTemp.Close()

								muxErr := video.MuxVideoWithAudio(ctx, generatedVideoPath, songPath, finalVideoPath, b.log)
								os.Remove(generatedVideoPath)
								if muxErr != nil {
									os.Remove(finalVideoPath)
									b.log.Errorf("handleIdeaGeneration: failed to mux audio into video: %v", muxErr)
									b.replyText(chatID, fmt.Sprintf("⚠️ Видео с музыкой не удалось собрать: %v", muxErr))
								} else {
									defer os.Remove(finalVideoPath)

									finalFile, err := os.Open(finalVideoPath)
									if err != nil {
										b.log.Errorf("handleIdeaGeneration: failed to open final video: %v", err)
										b.replyText(chatID, "⚠️ Не удалось открыть итоговый видеофайл")
									} else {
										defer finalFile.Close()

										videoMsg := tgbotapi.NewVideo(chatID, tgbotapi.FileReader{
											Name:   fmt.Sprintf("idea_%s.mp4", song.ID),
											Reader: finalFile,
										})
										videoMsg.Caption = fmt.Sprintf("🎬 Видео по идее: %s - %s", song.Author, song.Title)

										if _, err := b.tg.Send(videoMsg); err != nil {
											b.log.Errorf("handleIdeaGeneration: failed to send generated video: %v", err)
											b.replyText(chatID, "⚠️ Видео с музыкой собрано, но не удалось отправить в Telegram")
										}
									}
								}
							}
						}
					}
				}
			}
		}
	} else {
		b.replyText(chatID, "⚠️ BRATUHA_API_KEY не задан, поэтому видео не генерируется")
	}

	// Create file content with track info and ideas
	fileContent := fmt.Sprintf(
		"🎬 ВИДЕОИДЕЯ\n"+
			"══════════════════════════════════════════════════════════════\n\n"+
			"🎵 Трек: %s\n"+
			"👤 Артист: %s\n"+
			"⏱️ Длительность: %.1f сек\n\n"+
			"🤖 Bratuha operation: %s\n"+
			"🎞️ Video URL: %s\n\n"+
			"─────────────────────────────────────────────────────────────\n"+
			"📝 ВИДЕОКОНЦЕПЦИЯ\n"+
			"─────────────────────────────────────────────────────────────\n\n%s",
		song.Title,
		song.Author,
		song.DurationS,
		generatedOperationID,
		generatedVideoURL,
		scenesText,
	)

	// Upload file to S3
	s3Key := fmt.Sprintf("ideas/%s_idea.txt", song.ID)
	err = b.svc.GetS3Client().PutBytes(ctx, s3Key, []byte(fileContent), "text/plain")
	if err != nil {
		b.log.Errorf("handleIdeaGeneration: failed to save to S3: %v", err)
		b.replyText(chatID, "❌ Ошибка при сохранении файла идеи")
		return
	}

	b.log.Infof("handleIdeaGeneration: file uploaded to S3: %s", s3Key)

	// Send file to Telegram
	msg := tgbotapi.NewDocument(chatID, tgbotapi.FileReader{
		Name:   fmt.Sprintf("%s_%s_idea.txt", song.Author, song.Title),
		Reader: strings.NewReader(fileContent),
	})
	msg.Caption = fmt.Sprintf(
		"🎬 Видеоконцепция под трек: <b>%s</b> - <b>%s</b>",
		song.Author,
		song.Title,
	)
	msg.ParseMode = "HTML"

	_, err = b.tg.Send(msg)
	if err != nil {
		b.log.Errorf("handleIdeaGeneration: failed to send file: %v", err)
		b.replyText(chatID, "❌ Ошибка при отправке файла идеи")
		return
	}

	// Send AI prompt as a separate message for easy copying
	if aiPromptText != "" {
		promptMsg := fmt.Sprintf(
			"🤖 <b>Готовый промпт для ИИ-генерации видео</b>\n"+
				"<i>(Runway / Luma / Kling)</i>\n\n"+
				"<code>%s</code>",
			aiPromptText,
		)
		b.replyHTML(chatID, promptMsg)
	}

	b.log.Infof("handleIdeaGeneration: COMPLETE")
}

func (b *TelegramBot) handleIdeaList(ctx context.Context, chatID int64, offset int) {
	b.log.Infof("handleIdeaList: START - offset=%d", offset)

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
	b.log.Infof("handleIdeaList: showing %d songs (offset=%d, total=%d)", len(songs), offset, len(allSongs))

	// Create buttons for songs (max 8)
	rows := make([][]tgbotapi.InlineKeyboardButton, 0)
	for _, song := range songs {
		// Truncate title to fit button
		title := song.Title
		if len(title) > 30 {
			title = title[:27] + "..."
		}

		btn := tgbotapi.NewInlineKeyboardButtonData(
			title,
			fmt.Sprintf("ideagen:%s", song.ID),
		)
		rows = append(rows, []tgbotapi.InlineKeyboardButton{btn})
	}

	// Add pagination buttons
	navRow := make([]tgbotapi.InlineKeyboardButton, 0)
	if offset > 0 {
		navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData(
			"⬅️ Предыдущие",
			fmt.Sprintf("idealist:%d", offset-itemsPerPage),
		))
	}
	if endIdx < len(allSongs) {
		navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData(
			"➡️ Следующие",
			fmt.Sprintf("idealist:%d", endIdx),
		))
	}
	if len(navRow) > 0 {
		rows = append(rows, navRow)
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("📋 Выберите трек (%d-%d из %d):", offset+1, endIdx, len(allSongs)))
	msg.ReplyMarkup = keyboard
	b.tg.Send(msg)
}

func (b *TelegramBot) handleIdeaSearch(ctx context.Context, chatID int64, query string) {
	b.log.Infof("handleIdeaSearch: START - query=%s", query)

	// Search for songs matching the query
	songs, err := b.svc.SearchSongs(ctx, query)
	if err != nil {
		b.log.Errorf("handleIdeaSearch: search failed: %v", err)
		b.replyText(chatID, fmt.Sprintf("❌ Ошибка поиска: %v", err))
		return
	}

	if len(songs) == 0 {
		b.replyText(chatID, fmt.Sprintf("❌ Треков не найдено по запросу: \"%s\"", query))
		return
	}

	b.log.Infof("handleIdeaSearch: found %d songs matching \"%s\"", len(songs), query)

	// Create buttons for found songs (max 10)
	maxResults := 10
	displayCount := len(songs)
	if displayCount > maxResults {
		displayCount = maxResults
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0)
	for i := 0; i < displayCount; i++ {
		song := songs[i]
		// Show title and artist in one button
		btnText := song.Title
		if len(btnText) > 25 {
			btnText = btnText[:22] + "..."
		}

		btn := tgbotapi.NewInlineKeyboardButtonData(
			btnText,
			fmt.Sprintf("ideagen:%s", song.ID),
		)
		rows = append(rows, []tgbotapi.InlineKeyboardButton{btn})
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	resultMsg := fmt.Sprintf("🔍 Найдено %d треков по запросу \"%s\":\n\nВыберите трек:", displayCount, query)
	if len(songs) > maxResults {
		resultMsg += fmt.Sprintf("\n\n(показаны первые %d из %d)", displayCount, len(songs))
	}

	msg := tgbotapi.NewMessage(chatID, resultMsg)
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
