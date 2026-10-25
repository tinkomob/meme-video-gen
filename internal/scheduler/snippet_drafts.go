package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"os"
	"slices"
	"strconv"
	"time"

	"meme-video-gen/internal/model"
	"meme-video-gen/internal/uploaders"
)

func snippetDraftKey(id, extension string) (string, error) {
	if len(id) != 24 {
		return "", fmt.Errorf("invalid snippet draft ID")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", fmt.Errorf("invalid snippet draft ID")
	}
	return "snippet-drafts/" + id + extension, nil
}

func (s *Service) StoreSnippetDraft(ctx context.Context, path string, song *model.Song, chatID, userID int64, style string, variant int) (*model.SnippetDraft, error) {
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	draft := &model.SnippetDraft{ID: hex.EncodeToString(id[:]), ChatID: chatID, UserID: userID, Song: *song, Style: style, Variant: variant, Targets: map[string]string{}, CreatedAt: time.Now()}
	key, _ := snippetDraftKey(draft.ID, ".mp4")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := s.s3c.PutBytes(ctx, key, data, "video/mp4"); err != nil {
		return nil, err
	}
	if err := s.SaveSnippetDraft(ctx, draft); err != nil {
		_ = s.s3c.Delete(ctx, key)
		return nil, err
	}
	return draft, nil
}

func (s *Service) SaveSnippetDraft(ctx context.Context, draft *model.SnippetDraft) error {
	key, err := snippetDraftKey(draft.ID, ".json")
	if err != nil {
		return err
	}
	return s.s3c.WriteJSON(ctx, key, draft)
}

func (s *Service) GetSnippetDraft(ctx context.Context, id string) (*model.SnippetDraft, error) {
	key, err := snippetDraftKey(id, ".json")
	if err != nil {
		return nil, err
	}
	var draft model.SnippetDraft
	found, err := s.s3c.ReadJSON(ctx, key, &draft)
	if err != nil {
		return nil, err
	}
	if !found || draft.ID != id {
		return nil, fmt.Errorf("snippet draft not found")
	}
	if draft.Targets == nil {
		draft.Targets = map[string]string{}
	}
	return &draft, nil
}

func (s *Service) DeleteSnippetDraft(ctx context.Context, id string) error {
	video, err := snippetDraftKey(id, ".mp4")
	if err != nil {
		return err
	}
	metadata, _ := snippetDraftKey(id, ".json")
	if err := s.s3c.Delete(ctx, video); err != nil {
		return err
	}
	return s.s3c.Delete(ctx, metadata)
}

func (s *Service) prepareSnippetUploaders(ctx context.Context, platforms []string) {
	mgr := s.GetUploadersManager()
	if slices.Contains(platforms, "telegram") {
		if s.GetConfig().PostsChatID == 0 {
			if err := s.LoadPostsChatID(ctx); err != nil {
				s.log.Warnf("snippet: could not load posts chat: %v", err)
			}
		}
		if cfg := s.GetConfig(); cfg.PostsChatID != 0 {
			mgr.AddUploader("telegram", uploaders.NewTelegramUploader(cfg.TelegramToken, strconv.FormatInt(cfg.PostsChatID, 10)))
		}
	}
	if slices.Contains(platforms, "youtube") {
		if _, err := mgr.GetUploader("youtube"); err != nil {
			if err := s.InitializeYouTubeUploaderFromS3(ctx); err != nil {
				s.log.Warnf("snippet: YouTube uploader unavailable: %v", err)
			}
		}
	}
}

// Save an in-flight marker before each upload so a restart cannot blindly retry it.
func (s *Service) PublishSnippetDraft(ctx context.Context, draft *model.SnippetDraft, platforms []string) (map[string]*uploaders.UploadResult, error) {
	if draft.Targets == nil {
		draft.Targets = map[string]string{}
	}
	if len(platforms) == 0 {
		return nil, fmt.Errorf("select at least one platform")
	}
	for _, platform := range platforms {
		if platform != "telegram" && platform != "youtube" {
			return nil, fmt.Errorf("unsupported snippet platform")
		}
	}
	key, err := snippetDraftKey(draft.ID, ".mp4")
	if err != nil {
		return nil, err
	}
	if s.GetUploadersManager() == nil {
		return nil, fmt.Errorf("uploaders manager is not initialized")
	}
	path, err := s.DownloadFileToTemp(ctx, key, "snippet-publish")
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	s.prepareSnippetUploaders(ctx, platforms)
	title := draft.Song.Author + " — " + draft.Song.Title
	description := title
	request := &uploaders.UploadRequest{VideoPath: path, Title: title, Description: description, Caption: html.EscapeString(description), Privacy: "public", Silent: true}
	results := map[string]*uploaders.UploadResult{}
	for _, platform := range platforms {
		if draft.Targets[platform] == "sent" || draft.Targets[platform] == "sending" {
			continue
		}
		previous := draft.Targets[platform]
		draft.Targets[platform] = "sending"
		if err := s.SaveSnippetDraft(ctx, draft); err != nil {
			draft.Targets[platform] = previous
			return results, err
		}
		result, uploadErr := s.GetUploadersManager().Upload(ctx, platform, request)
		if result == nil {
			result = &uploaders.UploadResult{Platform: platform, Error: "no upload result"}
		}
		if uploadErr != nil {
			result.Success = false
			result.Error = uploadErr.Error()
		}
		results[platform] = result
		status := "failed"
		if result.Success {
			status = "sent"
		}
		draft.Targets[platform] = status
		if err := s.SaveSnippetDraft(ctx, draft); err != nil {
			draft.Targets[platform] = "sending"
			return results, fmt.Errorf("could not save publication result; check %s before retrying: %w", platform, err)
		}
	}
	return results, nil
}
