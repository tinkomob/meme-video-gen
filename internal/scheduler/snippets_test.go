package scheduler

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/s3"
	"meme-video-gen/internal/uploaders"
)

type snippetTestUploader struct {
	result  *uploaders.UploadResult
	request *uploaders.UploadRequest
}

func (u *snippetTestUploader) Platform() string { return "test" }
func (u *snippetTestUploader) Upload(_ context.Context, request *uploaders.UploadRequest) (*uploaders.UploadResult, error) {
	u.request = request
	return u.result, nil
}

func snippetTestService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	log, err := logging.New(filepath.Join(t.TempDir(), "errors.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	return &Service{log: log, uploadersManager: uploaders.NewManager(), cron: cron.New(cron.WithSeconds()), s3c: &snippetMemoryS3{}}
}

func TestSnippetUploadSilentAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name              string
		telegram, youtube *uploaders.UploadResult
		wantError         bool
	}{
		{"success", &uploaders.UploadResult{Success: true}, &uploaders.UploadResult{Success: true}, false},
		{"partial", &uploaders.UploadResult{Success: true}, &uploaders.UploadResult{Error: "failed"}, false},
		{"failed", &uploaders.UploadResult{Error: "failed"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := snippetTestService(t)
			tg, yt := &snippetTestUploader{result: tc.telegram}, &snippetTestUploader{result: tc.youtube}
			s.uploadersManager.AddUploader("telegram", tg)
			s.uploadersManager.AddUploader("youtube", yt)
			err := s.uploadSongSnippet(context.Background(), "test.mp4", &model.Song{Author: "A & B", Title: "<Night>", SourceURL: "https://example.com/?a=1&b=2"})
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v wantError=%v", err, tc.wantError)
			}
			for _, u := range []*snippetTestUploader{tg, yt} {
				if u.request == nil || !u.request.Silent || u.request.Privacy != "public" {
					t.Fatalf("incorrect upload request: %+v", u.request)
				}
				if strings.Contains(u.request.Caption, "<Night>") || !strings.Contains(u.request.Caption, "&lt;Night&gt;") {
					t.Fatalf("caption is not escaped: %s", u.request.Caption)
				}
				if !strings.Contains(u.request.Description, "<Night>") {
					t.Fatal("plain description changed")
				}
			}
		})
	}
}

func TestSnippetCronRegistration(t *testing.T) {
	s := snippetTestService(t)
	if err := s.registerSnippetSchedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.cron.Entries()) != 1 {
		t.Fatal("expected one snippet cron job")
	}
	now := time.Date(2026, 10, 25, 12, 34, 40, 0, time.UTC)
	if got := s.cron.Entries()[0].Schedule.Next(now); !got.Equal(now.Add(20 * time.Second)) {
		t.Fatalf("expected next minute, got %s", got)
	}
}

type snippetConfigS3 struct{ s3.Client }

func (snippetConfigS3) ReadJSON(_ context.Context, _ string, out any) (bool, error) {
	return true, json.Unmarshal([]byte(`{"posts_chat_id":-100123456789}`), out)
}
func TestLoadPostsChatIDAcceptsChannel(t *testing.T) {
	s := snippetTestService(t)
	s.s3c = snippetConfigS3{}
	if err := s.LoadPostsChatID(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.GetConfig().PostsChatID != -100123456789 {
		t.Fatal("negative channel ID was ignored")
	}
}
