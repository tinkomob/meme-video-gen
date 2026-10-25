package scheduler

import (
	"context"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"

	"meme-video-gen/internal/model"
	"meme-video-gen/internal/uploaders"
)

// GenerateSongSnippet returns a local file. The caller removes its temporary directory.
func (s *Service) GenerateSongSnippet(ctx context.Context, style string, variant int, songID string) (string, *model.Song, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "song-snippet-post-")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "snippet.mp4")
	song, err := s.snippetGen.GenerateSongSnippet(ctx, path, style, songID, variant)
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	return path, song, nil
}

func (s *Service) publishSongSnippet(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	mgr := s.GetUploadersManager()
	if mgr == nil {
		return fmt.Errorf("uploaders manager is not initialized")
	}
	s.prepareSnippetUploaders(ctx, []string{"telegram", "youtube"})
	if len(mgr.AvailablePlatforms()) == 0 {
		return fmt.Errorf("no snippet uploaders configured")
	}
	path, song, err := s.GenerateSongSnippet(ctx, "random", -1, "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(filepath.Dir(path))
	return s.uploadSongSnippet(ctx, path, song)
}

func (s *Service) uploadSongSnippet(ctx context.Context, path string, song *model.Song) error {
	title := song.Author + " — " + song.Title
	description := title
	if song.SourceURL != "" {
		description += "\n" + song.SourceURL
	}
	results := s.GetUploadersManager().UploadToAll(ctx, &uploaders.UploadRequest{
		VideoPath: path, Title: title, Description: description, Caption: html.EscapeString(description), Privacy: "public", Silent: true,
	})
	success := 0
	var failures []string
	for platform, result := range results {
		if result != nil && result.Success {
			success++
			s.log.Infof("snippet published to %s", platform)
		} else {
			detail := "no upload result"
			if result != nil {
				detail = result.Error
			}
			failures = append(failures, platform+": "+detail)
			s.log.Errorf("snippet upload to %s: %s", platform, detail)
		}
	}
	if success == 0 {
		return fmt.Errorf("snippet not published: %s", strings.Join(failures, "; "))
	}
	return nil
}
