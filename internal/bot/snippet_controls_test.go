package bot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/uploaders"
)

const testDraftID = "0123456789abcdef01234567"

func TestSnippetControls(t *testing.T) {
	draft := &model.SnippetDraft{ID: testDraftID, Selected: []string{"telegram"}, Targets: map[string]string{"youtube": "sent"}}
	keyboard := snippetControls(draft)
	if len(keyboard.InlineKeyboard) != 4 {
		t.Fatal("missing control rows")
	}
	var actions []string
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			if button.CallbackData == nil {
				t.Fatal("button has no action")
			}
			action, id, _, err := parseSnippetCallback(*button.CallbackData)
			if err != nil || id != draft.ID {
				t.Fatalf("invalid button %q: %v", *button.CallbackData, err)
			}
			actions = append(actions, action)
		}
	}
	if got := strings.Join(actions, ","); got != "telegram,status,send,all,visual,random,list,delete" {
		t.Fatalf("controls: %s", got)
	}
	if !strings.Contains(keyboard.InlineKeyboard[0][0].Text, "☑️") || !strings.Contains(keyboard.InlineKeyboard[0][1].Text, "✅") {
		t.Fatal("selection and publication status are missing")
	}
	for _, bad := range []string{"snip:send:../../bad", "snip:unknown:" + testDraftID, "snip:list:" + testDraftID + ":-1", "snip:track:" + testDraftID + ":../../file", "snip:all:" + testDraftID + ":extra"} {
		if _, _, _, err := parseSnippetCallback(bad); err == nil {
			t.Fatalf("accepted invalid callback: %s", bad)
		}
	}
}

func TestSnippetSendingStatusReplacedByResult(t *testing.T) {
	events := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test"}}`)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "sendMessage"):
			if r.Form.Get("text") != "Отправляю…" || r.Form.Get("disable_notification") != "true" {
				t.Errorf("incorrect progress message: %v", r.Form)
			}
			events <- "message"
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":77,"chat":{"id":123,"type":"private"}}}`)
		case strings.HasSuffix(r.URL.Path, "editMessageText"):
			if r.Form.Get("message_id") != "77" || r.Form.Get("text") != "✅ Instagram\n✅ YouTube" {
				t.Errorf("incorrect result edit: %v", r.Form)
			}
			events <- "edit"
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":77,"chat":{"id":123,"type":"private"}}}`)
		default:
			t.Errorf("unexpected API request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	api, err := tgbotapi.NewBotAPIWithClient("test-token", server.URL+"/bot%s/%s", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = (&TelegramBot{tg: api}).withSnippetPublicationStatus(123, func() string {
		events <- "publish"
		return snippetPublicationResult(map[string]*uploaders.UploadResult{"telegram": {Success: true}, "youtube": {Success: true}}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"message", "publish", "edit"} {
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("got %s want %s", got, want)
			}
		default:
			t.Fatalf("missing event %s", want)
		}
	}
}

func TestSnippetOwnerAndBusyGuard(t *testing.T) {
	draft := &model.SnippetDraft{ChatID: 123, UserID: 456}
	cb := &tgbotapi.CallbackQuery{From: &tgbotapi.User{ID: 456}, Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 123}}}
	if !snippetOwner(draft, cb) {
		t.Fatal("author was rejected")
	}
	cb.From.ID = 789
	if snippetOwner(draft, cb) {
		t.Fatal("another user may operate the draft")
	}
	cb.From.ID, cb.Message.Chat.ID = 456, 789
	if snippetOwner(draft, cb) {
		t.Fatal("forwarded controls may operate the draft")
	}
	b := &TelegramBot{}
	if !b.claimSnippetAction(testDraftID) || b.claimSnippetAction(testDraftID) {
		t.Fatal("double click was not blocked")
	}
	b.releaseSnippetAction(testDraftID)
	if !b.claimSnippetAction(testDraftID) {
		t.Fatal("action lock was not released")
	}
}

func TestSnippetOtherSongChoices(t *testing.T) {
	songs := []*model.Song{{ID: "AAAAAAAAAAA", AudioKey: "a", DurationS: 30}, {ID: "BBBBBBBBBBB", AudioKey: "b", DurationS: 20}, {ID: "CCCCCCCCCCC", AudioKey: "c", DurationS: 5}, {ID: "DDDDDDDDDDD"}, nil}
	choices := snippetSongChoices(songs, "AAAAAAAAAAA")
	if len(choices) != 1 || choices[0].ID != "BBBBBBBBBBB" {
		t.Fatalf("unexpected replacement choices: %+v", choices)
	}
}

func TestSnippetPreviewHasSilentVideoAndControls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snippet.mp4")
	if err := os.WriteFile(path, []byte("preview video"), 0600); err != nil {
		t.Fatal(err)
	}
	received := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test"}}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "sendVideo") {
			t.Errorf("unexpected API method: %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("disable_notification") != "true" || r.FormValue("supports_streaming") != "true" || r.FormValue("chat_id") != "123" {
			t.Error("incorrect video flags")
		}
		if r.FormValue("duration") != "20" {
			t.Error("preview does not declare 20 seconds")
		}
		if !strings.Contains(r.FormValue("caption"), "&lt;Night&gt;") {
			t.Error("caption was not escaped")
		}
		var keyboard tgbotapi.InlineKeyboardMarkup
		if err := json.Unmarshal([]byte(r.FormValue("reply_markup")), &keyboard); err != nil || len(keyboard.InlineKeyboard) != 4 {
			t.Errorf("video has no controls: %v", err)
		}
		if len(r.MultipartForm.File["video"]) != 1 {
			t.Error("video not attached")
		}
		received <- true
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":123,"type":"private"}}}`)
	}))
	defer server.Close()
	api, err := tgbotapi.NewBotAPIWithClient("test-token", server.URL+"/bot%s/%s", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	draft := &model.SnippetDraft{ID: testDraftID, ChatID: 123, UserID: 456, Song: model.Song{Title: "<Night>", Author: "A & B"}}
	if err := (&TelegramBot{tg: api}).sendSnippetPreview(draft, path); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
	default:
		t.Fatal("preview was not sent")
	}
}
