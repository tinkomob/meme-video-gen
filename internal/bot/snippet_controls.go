package bot

import (
	"context"
	"encoding/hex"
	"fmt"
	"html"
	"math/rand"
	"slices"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/snippet"
	"meme-video-gen/internal/uploaders"
)

var snippetPlatformNames = map[string]string{"telegram": "Instagram", "youtube": "YouTube"}

func snippetButton(text, action, id string) tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardButtonData(text, "snip:"+action+":"+id)
}

func snippetControls(d *model.SnippetDraft) tgbotapi.InlineKeyboardMarkup {
	var targets []tgbotapi.InlineKeyboardButton
	for _, platform := range []string{"telegram", "youtube"} {
		label, icon, action := snippetPlatformNames[platform], "⬜", platform
		if slices.Contains(d.Selected, platform) {
			icon = "☑️"
		}
		switch d.Targets[platform] {
		case "sent":
			icon, action = "✅", "status"
		case "sending":
			icon, action = "⏳", "status"
		case "failed":
			label += " · ошибка"
		}
		targets = append(targets, snippetButton(icon+" "+label, action, d.ID))
	}
	return tgbotapi.NewInlineKeyboardMarkup(
		targets,
		tgbotapi.NewInlineKeyboardRow(snippetButton("📤 Отправить выбранные", "send", d.ID), snippetButton("📤 Во все", "all", d.ID)),
		tgbotapi.NewInlineKeyboardRow(snippetButton("🎨 Новый визуал", "visual", d.ID), snippetButton("🎲 Другая песня", "random", d.ID)),
		tgbotapi.NewInlineKeyboardRow(snippetButton("📋 Выбрать песню", "list", d.ID+":0"), snippetButton("🗑 Удалить черновик", "delete", d.ID)),
	)
}

func parseSnippetCallback(data string) (action, id, value string, err error) {
	parts := strings.Split(data, ":")
	if len(data) > 64 || len(parts) < 3 || len(parts) > 4 || parts[0] != "snip" || len(parts[2]) != 24 {
		return "", "", "", fmt.Errorf("invalid snippet callback")
	}
	if _, err := hex.DecodeString(parts[2]); err != nil {
		return "", "", "", err
	}
	action, id = parts[1], parts[2]
	switch action {
	case "telegram", "youtube", "send", "all", "visual", "random", "delete", "back", "status":
		if len(parts) != 3 {
			return "", "", "", fmt.Errorf("unexpected callback argument")
		}
	case "list", "track":
		if len(parts) != 4 {
			return "", "", "", fmt.Errorf("missing callback argument")
		}
		value = parts[3]
		if action == "list" {
			if n, e := strconv.Atoi(value); e != nil || n < 0 {
				return "", "", "", fmt.Errorf("invalid page")
			}
		}
		if action == "track" && (len(value) != 11 || strings.Trim(value, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "") {
			return "", "", "", fmt.Errorf("invalid song ID")
		}
	default:
		return "", "", "", fmt.Errorf("unsupported snippet action")
	}
	return action, id, value, nil
}

func (b *TelegramBot) claimSnippetAction(id string) bool {
	b.snippetActionMux.Lock()
	defer b.snippetActionMux.Unlock()
	if b.snippetActions == nil {
		b.snippetActions = map[string]bool{}
	}
	if b.snippetActions[id] {
		return false
	}
	b.snippetActions[id] = true
	return true
}

func (b *TelegramBot) releaseSnippetAction(id string) {
	b.snippetActionMux.Lock()
	defer b.snippetActionMux.Unlock()
	delete(b.snippetActions, id)
}

func snippetOwner(d *model.SnippetDraft, cb *tgbotapi.CallbackQuery) bool {
	return cb != nil && cb.From != nil && cb.Message != nil && cb.Message.Chat != nil && d.ChatID == cb.Message.Chat.ID && d.UserID == cb.From.ID
}

func (b *TelegramBot) snippetActionError(chatID int64, err error) {
	b.log.Errorf("snippet action: %s", strings.ReplaceAll(err.Error(), b.tg.Token, "[redacted]"))
	b.replyHTMLSilent(chatID, "Не удалось выполнить действие. "+html.EscapeString(strings.ReplaceAll(err.Error(), b.tg.Token, "[redacted]")), true)
}

func (b *TelegramBot) updateSnippetControls(message *tgbotapi.Message, draft *model.SnippetDraft) error {
	_, err := b.tg.Request(tgbotapi.NewEditMessageReplyMarkup(message.Chat.ID, message.MessageID, snippetControls(draft)))
	return err
}

func (b *TelegramBot) handleSnippetCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	action, id, value, err := parseSnippetCallback(cb.Data)
	answer := func(text string) { b.tg.Request(tgbotapi.NewCallback(cb.ID, text)) }
	if err != nil {
		answer("Некорректная кнопка.")
		return
	}
	if !b.claimSnippetAction(id) {
		answer("Действие уже выполняется.")
		return
	}
	go func() {
		defer b.releaseSnippetAction(id)
		loadCtx, cancelLoad := context.WithTimeout(ctx, 5*time.Second)
		draft, err := b.svc.GetSnippetDraft(loadCtx, id)
		cancelLoad()
		if err != nil {
			answer("Сниппет удалён или временно недоступен.")
			return
		}
		if !snippetOwner(draft, cb) {
			answer("Кнопки доступны автору сниппета в исходном чате.")
			return
		}
		answer("")
		actionCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		switch action {
		case "telegram", "youtube":
			if draft.Targets[action] == "sent" || draft.Targets[action] == "sending" {
				return
			}
			if slices.Contains(draft.Selected, action) {
				draft.Selected = slices.DeleteFunc(draft.Selected, func(p string) bool { return p == action })
			} else {
				draft.Selected = append(draft.Selected, action)
			}
			err = b.svc.SaveSnippetDraft(actionCtx, draft)
			if err == nil {
				err = b.updateSnippetControls(cb.Message, draft)
			}
		case "send", "all":
			platforms := draft.Selected
			if action == "all" {
				platforms = []string{"telegram", "youtube"}
			}
			if len(platforms) == 0 {
				b.replyHTMLSilent(draft.ChatID, "Сначала выберите Instagram или YouTube.", true)
				return
			}
			var controlsErr error
			err = b.withSnippetPublicationStatus(draft.ChatID, func() string {
				results, publishErr := b.svc.PublishSnippetDraft(actionCtx, draft, platforms)
				controlsErr = b.updateSnippetControls(cb.Message, draft)
				if publishErr != nil {
					b.log.Errorf("snippet publication: %s", strings.ReplaceAll(publishErr.Error(), b.tg.Token, "[redacted]"))
				}
				return snippetPublicationResult(results, publishErr)
			})
			if err == nil {
				err = controlsErr
			}
		case "visual":
			b.startSnippetGeneration(ctx, draft.ChatID, draft.UserID, "random", -1, draft.Song.ID)
		case "random":
			var songs []*model.Song
			songs, err = b.svc.GetAllSongs(actionCtx)
			if err == nil {
				pool := snippetSongChoices(songs, draft.Song.ID)
				if len(pool) == 0 {
					err = fmt.Errorf("в библиотеке нет другой подходящей песни")
				} else {
					b.startSnippetGeneration(ctx, draft.ChatID, draft.UserID, draft.Style, draft.Variant, pool[rand.Intn(len(pool))].ID)
				}
			}
		case "list":
			offset, _ := strconv.Atoi(value)
			err = b.showSnippetSongs(actionCtx, cb.Message, draft, offset)
		case "track":
			err = b.updateSnippetControls(cb.Message, draft)
			if err == nil {
				b.startSnippetGeneration(ctx, draft.ChatID, draft.UserID, draft.Style, draft.Variant, value)
			}
		case "back":
			err = b.updateSnippetControls(cb.Message, draft)
		case "delete":
			err = b.svc.DeleteSnippetDraft(actionCtx, draft.ID)
			if err == nil {
				_, err = b.tg.Request(tgbotapi.NewDeleteMessage(draft.ChatID, cb.Message.MessageID))
				if err != nil {
					b.tg.Request(tgbotapi.NewEditMessageReplyMarkup(draft.ChatID, cb.Message.MessageID, tgbotapi.NewInlineKeyboardMarkup()))
					b.replyHTMLSilent(draft.ChatID, "Черновик удалён. Старое сообщение можно удалить вручную.", true)
					err = nil
				}
			}
		case "status":
			b.replyHTMLSilent(draft.ChatID, "✅ — уже отправлено. ⏳ — отправка была начата; после сбоя проверьте результат на площадке. Автоповтора не будет.", true)
		}
		if err != nil {
			b.snippetActionError(draft.ChatID, err)
		}
	}()
}

func snippetPublicationResult(results map[string]*uploaders.UploadResult, publishErr error) string {
	var lines []string
	for _, platform := range []string{"telegram", "youtube"} {
		if result := results[platform]; result != nil {
			name := snippetPlatformNames[platform]
			if result.Success {
				lines = append(lines, "✅ "+name)
			} else {
				lines = append(lines, "❌ "+name+": "+html.EscapeString(result.Error))
			}
		}
	}
	if publishErr != nil {
		lines = append(lines, "❌ "+html.EscapeString(publishErr.Error()))
	}
	if len(lines) == 0 {
		return "Выбранные площадки уже обработаны. Повторной отправки не будет."
	}
	return strings.Join(lines, "\n")
}

func (b *TelegramBot) withSnippetPublicationStatus(chatID int64, publish func() string) error {
	message := tgbotapi.NewMessage(chatID, "Отправляю…")
	message.DisableNotification = true
	sent, err := b.tg.Send(message)
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(publish(), b.tg.Token, "[redacted]")
	edit := tgbotapi.NewEditMessageText(chatID, sent.MessageID, text)
	edit.ParseMode = tgbotapi.ModeHTML
	_, err = b.tg.Request(edit)
	return err
}

func snippetSongChoices(songs []*model.Song, excludeID string) []*model.Song {
	var choices []*model.Song
	for _, song := range songs {
		if song != nil && song.ID != excludeID && song.AudioKey != "" && (song.DurationS == 0 || song.DurationS >= snippet.DurationSeconds) && len(song.ID) == 11 && strings.Trim(song.ID, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") == "" {
			choices = append(choices, song)
		}
	}
	return choices
}

func (b *TelegramBot) showSnippetSongs(ctx context.Context, message *tgbotapi.Message, draft *model.SnippetDraft, offset int) error {
	songs, err := b.svc.GetAllSongs(ctx)
	if err != nil {
		return err
	}
	songs = snippetSongChoices(songs, "")
	if len(songs) == 0 {
		return fmt.Errorf("в библиотеке нет подходящих песен")
	}
	if offset >= len(songs) {
		offset = 0
	}
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, song := range songs[offset:min(offset+8, len(songs))] {
		label := []rune(song.Author + " — " + song.Title)
		if len(label) > 48 {
			label = append(label[:47], '…')
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(snippetButton(string(label), "track", draft.ID+":"+song.ID)))
	}
	var nav []tgbotapi.InlineKeyboardButton
	if offset > 0 {
		nav = append(nav, snippetButton("←", "list", draft.ID+":"+strconv.Itoa(max(0, offset-8))))
	}
	if offset+8 < len(songs) {
		nav = append(nav, snippetButton("→", "list", draft.ID+":"+strconv.Itoa(offset+8)))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(snippetButton("Назад", "back", draft.ID)))
	_, err = b.tg.Request(tgbotapi.NewEditMessageReplyMarkup(message.Chat.ID, message.MessageID, tgbotapi.NewInlineKeyboardMarkup(rows...)))
	return err
}
