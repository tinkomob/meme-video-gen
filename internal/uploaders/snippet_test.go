package uploaders

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type snippetTransport func(*http.Request) (*http.Response, error)

func (f snippetTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTelegramSnippetDisableNotification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, []byte("test video"), 0600); err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, silent := range []bool{true, false} {
		called := false
		http.DefaultTransport = snippetTransport(func(r *http.Request) (*http.Response, error) {
			called = true
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			want := ""
			if silent {
				want = "true"
			}
			if r.FormValue("disable_notification") != want || r.FormValue("chat_id") != "-100123" {
				t.Errorf("incorrect Telegram fields: %v", r.MultipartForm.Value)
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
		})
		result, err := NewTelegramUploader("test-token", "-100123").Upload(context.Background(), &UploadRequest{VideoPath: path, Caption: "Test", Silent: silent})
		if err != nil || result == nil || !result.Success || !called {
			t.Fatalf("upload result=%+v err=%v called=%v", result, err, called)
		}
	}
	http.DefaultTransport = snippetTransport(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	result, err := NewTelegramUploader("test-token", "-100123").Upload(context.Background(), &UploadRequest{VideoPath: path, Silent: true})
	if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "test-token") || strings.Contains(result.Error, "test-token") {
		t.Fatalf("unsafe or lost network error: %v", err)
	}
}

type snippetStubUploader struct{}

func TestManagerPublicationTargets(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	t.Setenv("POSTS_CHAT_ID", "-100123")
	t.Setenv("POSTS_CHATID", "")
	m := NewManager()
	platforms := m.AvailablePlatforms()
	if len(platforms) != 1 || platforms[0] != "telegram" {
		t.Fatalf("unexpected automatic targets: %v", platforms)
	}
	m.AddUploader("youtube", snippetStubUploader{})
	if len(m.AvailablePlatforms()) != 2 {
		t.Fatal("YouTube registration failed")
	}
}

func TestManagerDestinationPriority(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	t.Setenv("POSTS_CHAT_ID", "-100123")
	t.Setenv("POSTS_CHATID", "456")
	uploader, err := NewManager().GetUploader("telegram")
	if err != nil || uploader.(*TelegramUploader).chatID != "-100123" {
		t.Fatal("legacy destination replaced canonical configuration")
	}
}

func (snippetStubUploader) Platform() string { return "stub" }
func (snippetStubUploader) Upload(context.Context, *UploadRequest) (*UploadResult, error) {
	return &UploadResult{Success: true}, nil
}

func TestManagerConcurrentRegistrationAndUpload(t *testing.T) {
	m := &Manager{uploaders: map[string]Uploader{"stub": snippetStubUploader{}}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.AddUploader("stub", snippetStubUploader{})
				if r := m.UploadToAll(context.Background(), &UploadRequest{Silent: true})["stub"]; r == nil || !r.Success {
					t.Error("lost uploader during registration")
				}
			}
		}()
	}
	wg.Wait()
}
