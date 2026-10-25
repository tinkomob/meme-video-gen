package bot

import (
	"context"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/snippet"
)

func snippetCommandArgs(args string) (string, int, error) {
	parts := strings.Fields(args)
	style, variant := "random", -1
	if len(parts) > 2 {
		return "", 0, fmt.Errorf("использование: /snippet [стиль] [0|1|2]")
	}
	if len(parts) > 0 {
		style = strings.ToLower(parts[0])
	}
	switch style {
	case "random", "silver", "violet", "amber", "aurora", "prism", "galaxy":
	default:
		return "", 0, fmt.Errorf("стили: random, silver, violet, amber, aurora, prism, galaxy")
	}
	if len(parts) == 2 {
		var err error
		variant, err = strconv.Atoi(parts[1])
		if err != nil || variant < 0 || variant > 2 {
			return "", 0, fmt.Errorf("вариант: 0, 1 или 2")
		}
	}
	return style, variant, nil
}

func (b *TelegramBot) cmdSnippet(ctx context.Context, chatID, userID int64, args string) {
	style, variant, err := snippetCommandArgs(args)
	if err != nil {
		b.replyHTMLSilent(chatID, html.EscapeString(err.Error()), true)
		return
	}
	b.startSnippetGeneration(ctx, chatID, userID, style, variant, "")
}

func (b *TelegramBot) startSnippetGeneration(ctx context.Context, chatID, userID int64, style string, variant int, songID string) {
	if !b.snippetRunning.CompareAndSwap(false, true) {
		b.replyHTMLSilent(chatID, "Сниппет уже генерируется. Дождитесь завершения.", true)
		return
	}
	b.replyHTMLSilent(chatID, fmt.Sprintf("Генерирую %d-секундный сниппет…", snippet.DurationSeconds), true)
	go func() {
		defer b.snippetRunning.Store(false)
		ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		path, song, err := b.svc.GenerateSongSnippet(ctx, style, variant, songID)
		if err == nil {
			defer os.RemoveAll(filepath.Dir(path))
			var draft *model.SnippetDraft
			draft, err = b.svc.StoreSnippetDraft(ctx, path, song, chatID, userID, style, variant)
			if err == nil {
				err = b.sendSnippetPreview(draft, path)
			}
		}
		if err != nil {
			b.log.Errorf("manual song snippet: %s", strings.ReplaceAll(err.Error(), b.tg.Token, "[redacted]"))
			b.replyHTMLSilent(chatID, "Не удалось создать или отправить сниппет. Подробности записаны в журнал.", true)
		}
	}()
}

func (b *TelegramBot) sendSnippetPreview(draft *model.SnippetDraft, path string) error {
	caption := draft.Song.Author + " — " + draft.Song.Title
	video := tgbotapi.NewVideo(draft.ChatID, tgbotapi.FilePath(path))
	video.Caption, video.ParseMode = html.EscapeString(caption), tgbotapi.ModeHTML
	video.DisableNotification, video.SupportsStreaming = true, true
	video.Duration = snippet.DurationSeconds
	video.ReplyMarkup = snippetControls(draft)
	_, err := b.tg.Send(video)
	return err
}
