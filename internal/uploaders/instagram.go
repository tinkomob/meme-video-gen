package uploaders

import (
	"context"
	"strconv"
)

// InstagramUploader sends videos to Telegram posts channel
type InstagramUploader struct {
	telegram *TelegramUploader
}

// NewInstagramUploader creates a new Instagram uploader that uses Telegram
func NewInstagramUploader(botToken string, postsChatID int64) *InstagramUploader {
	return &InstagramUploader{
		telegram: NewTelegramUploader(botToken, strconv.FormatInt(postsChatID, 10)),
	}
}

// Platform returns the platform name
func (i *InstagramUploader) Platform() string {
	return "instagram"
}

// Upload sends video to Telegram posts channel
func (i *InstagramUploader) Upload(ctx context.Context, req *UploadRequest) (*UploadResult, error) {
	return i.telegram.Upload(ctx, req)
}
