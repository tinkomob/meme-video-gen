package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestSnippetCommandArgs(t *testing.T) {
	for _, tc := range []struct {
		args, style string
		variant     int
		bad         bool
	}{
		{"", "random", -1, false}, {"galaxy 2", "galaxy", 2, false}, {"VIOLET 0", "violet", 0, false},
		{"aurora", "aurora", -1, false}, {"tunnel 0", "tunnel", 0, false}, {"orbit 1", "orbit", 1, false}, {"wave 2", "wave", 2, false}, {"rain", "rain", -1, false}, {"random 1", "random", 1, false},
		{"bad", "", 0, true}, {"galaxy 3", "", 0, true}, {"prism -1", "", 0, true}, {"silver x", "", 0, true}, {"amber 1 extra", "", 0, true},
	} {
		style, variant, err := snippetCommandArgs(tc.args)
		if (err != nil) != tc.bad || (!tc.bad && (style != tc.style || variant != tc.variant)) {
			t.Fatalf("%q: got %s/%d/%v", tc.args, style, variant, err)
		}
	}
}

func TestSnippetCommandRepliesAreSilent(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test","username":"test_bot"}}`)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("disable_notification") != "true" {
			t.Errorf("reply is not silent: %v", r.Form)
		}
		count.Add(1)
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":123,"type":"private"}}}`)
	}))
	defer server.Close()
	api, err := tgbotapi.NewBotAPIWithClient("test-token", server.URL+"/bot%s/%s", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	b := &TelegramBot{tg: api}
	b.cmdSnippet(context.Background(), 123, 123, "unknown")
	b.snippetRunning.Store(true)
	b.cmdSnippet(context.Background(), 123, 123, "")
	if count.Load() != 2 {
		t.Fatalf("expected two silent replies, got %d", count.Load())
	}
}

func TestCommandMenu(t *testing.T) {
	registered := make(chan []tgbotapi.BotCommand, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test","username":"test_bot"}}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "setMyCommands") {
			t.Errorf("unexpected API method: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		var commands []tgbotapi.BotCommand
		if err := json.Unmarshal([]byte(r.Form.Get("commands")), &commands); err != nil {
			t.Error(err)
		}
		registered <- commands
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	}))
	defer server.Close()
	api, err := tgbotapi.NewBotAPIWithClient("test-token", server.URL+"/bot%s/%s", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	(&TelegramBot{tg: api}).updateCommandMenu()
	select {
	case commands := <-registered:
		var names []string
		for _, command := range commands {
			names = append(names, command.Command)
		}
		if got := strings.Join(names, ","); got != "start,help,snippet,song,chatid" {
			t.Fatalf("unexpected command menu: %s", got)
		}
	default:
		t.Fatal("command menu was not registered")
	}
}
