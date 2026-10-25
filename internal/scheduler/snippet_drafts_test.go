package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meme-video-gen/internal/model"
	"meme-video-gen/internal/s3"
	"meme-video-gen/internal/uploaders"
)

type draftMemoryS3 struct {
	s3.Client
	objects           map[string][]byte
	writes, failWrite int
}

type destinationTransport func(*http.Request) (*http.Response, error)

func (f destinationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSnippetDestinationEnvironmentWinsOverSavedPreviewChat(t *testing.T) {
	s, m, draft, _, _ := draftFixture(t)
	s.cfg.PostsChatID, s.cfg.TelegramToken = -1003668452980, "test-token"
	draft.ChatID = 1084904013
	m.objects["config.json"] = []byte(`{"posts_chat_id":1084904013}`)
	if err := s.LoadPostsChatID(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.GetConfig().PostsChatID != -1003668452980 {
		t.Fatal("saved preview chat replaced configured destination")
	}
	originalTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	called := false
	http.DefaultTransport = destinationTransport(func(r *http.Request) (*http.Response, error) {
		called = true
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("chat_id") != "-1003668452980" || r.FormValue("disable_notification") != "true" {
			t.Errorf("wrong publication destination/flags: %v", r.MultipartForm.Value)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
	})
	results, err := s.PublishSnippetDraft(context.Background(), draft, []string{"telegram"})
	if err != nil || !called || !results["telegram"].Success {
		t.Fatalf("publication failed: %v", err)
	}
}

func (m *draftMemoryS3) PutBytes(_ context.Context, key string, data []byte, _ string) error {
	m.objects[key] = append([]byte(nil), data...)
	return nil
}
func (m *draftMemoryS3) GetReader(_ context.Context, key string) (*s3.ObjectReader, error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &s3.ObjectReader{Reader: io.NopCloser(bytes.NewReader(data)), Size: int64(len(data))}, nil
}
func (m *draftMemoryS3) Delete(_ context.Context, key string) error {
	delete(m.objects, key)
	return nil
}
func (m *draftMemoryS3) ReadJSON(_ context.Context, key string, out any) (bool, error) {
	data, ok := m.objects[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(data, out)
}
func (m *draftMemoryS3) WriteJSON(_ context.Context, key string, value any) error {
	m.writes++
	if m.writes == m.failWrite {
		return fmt.Errorf("storage unavailable")
	}
	data, err := json.Marshal(value)
	if err == nil {
		m.objects[key] = data
	}
	return err
}

type draftUploader struct {
	t     *testing.T
	calls int
	fail  bool
}

func (u *draftUploader) Platform() string { return "test" }
func (u *draftUploader) Upload(_ context.Context, request *uploaders.UploadRequest) (*uploaders.UploadResult, error) {
	u.calls++
	data, err := os.ReadFile(request.VideoPath)
	if err != nil || string(data) != "original snippet" {
		u.t.Error("publication changed video")
	}
	if !request.Silent || !strings.Contains(request.Caption, "&lt;Night&gt;") {
		u.t.Error("incorrect publication flags or caption")
	}
	return &uploaders.UploadResult{Success: !u.fail, Error: "test failure"}, nil
}

func draftFixture(t *testing.T) (*Service, *draftMemoryS3, *model.SnippetDraft, *draftUploader, *draftUploader) {
	t.Helper()
	s := snippetTestService(t)
	m := &draftMemoryS3{objects: map[string][]byte{}}
	s.s3c = m
	path := filepath.Join(t.TempDir(), "snippet.mp4")
	if err := os.WriteFile(path, []byte("original snippet"), 0600); err != nil {
		t.Fatal(err)
	}
	draft, err := s.StoreSnippetDraft(context.Background(), path, &model.Song{ID: "AAAAAAAAAAA", Title: "<Night>", Author: "A & B"}, 123, 456, "random", -1)
	if err != nil {
		t.Fatal(err)
	}
	tg, yt := &draftUploader{t: t}, &draftUploader{t: t}
	s.uploadersManager.AddUploader("telegram", tg)
	s.uploadersManager.AddUploader("youtube", yt)
	return s, m, draft, tg, yt
}

func TestSnippetDraftPublishSelectedAndRestart(t *testing.T) {
	s, _, draft, tg, yt := draftFixture(t)
	ctx := context.Background()
	draft.Selected = []string{"telegram"}
	if err := s.SaveSnippetDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetSnippetDraft(ctx, draft.ID)
	if err != nil || loaded.ChatID != 123 || loaded.UserID != 456 || len(loaded.Selected) != 1 {
		t.Fatalf("draft did not survive reload: %v", err)
	}
	results, err := s.PublishSnippetDraft(ctx, loaded, loaded.Selected)
	if err != nil || !results["telegram"].Success || tg.calls != 1 || yt.calls != 0 {
		t.Fatalf("incorrect destination: %v", err)
	}
	reloaded, err := s.GetSnippetDraft(ctx, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Targets["telegram"] != "sent" {
		t.Fatal("publication was not persisted")
	}
	if _, err := s.PublishSnippetDraft(ctx, reloaded, []string{"telegram"}); err != nil || tg.calls != 1 {
		t.Fatal("repeated a successful upload")
	}
	if _, err := s.PublishSnippetDraft(ctx, reloaded, []string{"telegram", "youtube"}); err != nil || tg.calls != 1 || yt.calls != 1 {
		t.Fatal("publish all repeated an upload or missed a target")
	}
}

func TestSnippetDraftPublicationFailureState(t *testing.T) {
	for _, afterUpload := range []bool{false, true} {
		s, m, draft, tg, _ := draftFixture(t)
		ctx := context.Background()
		m.failWrite = m.writes + 1
		if afterUpload {
			m.failWrite++
		}
		if _, err := s.PublishSnippetDraft(ctx, draft, []string{"telegram"}); err == nil {
			t.Fatal("lost storage error")
		}
		loaded, err := s.GetSnippetDraft(ctx, draft.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !afterUpload && tg.calls != 0 {
			t.Fatal("sent before saving the claim")
		}
		if afterUpload {
			if loaded.Targets["telegram"] != "sending" {
				t.Fatal("uncertain upload may be retried")
			}
			if _, err := s.PublishSnippetDraft(ctx, loaded, []string{"telegram"}); err != nil || tg.calls != 1 {
				t.Fatal("repeated an uncertain upload")
			}
		}
	}
}

func TestSnippetDraftRetryAndDeletion(t *testing.T) {
	s, m, draft, tg, _ := draftFixture(t)
	ctx := context.Background()
	tg.fail = true
	if _, err := s.PublishSnippetDraft(ctx, draft, []string{"telegram"}); err != nil || draft.Targets["telegram"] != "failed" {
		t.Fatal("failure state not saved")
	}
	tg.fail = false
	if _, err := s.PublishSnippetDraft(ctx, draft, []string{"telegram"}); err != nil || tg.calls != 2 {
		t.Fatal("explicit retry did not work")
	}
	if _, err := s.PublishSnippetDraft(ctx, draft, []string{"unknown"}); err == nil {
		t.Fatal("accepted an unsupported destination")
	}
	if err := s.DeleteSnippetDraft(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	if len(m.objects) != 0 {
		t.Fatal("draft video or metadata remained")
	}
	if _, err := s.GetSnippetDraft(ctx, draft.ID); err == nil {
		t.Fatal("deleted draft still available")
	}
	if err := s.DeleteSnippetDraft(ctx, "../../song"); err == nil {
		t.Fatal("accepted invalid draft ID")
	}
}
