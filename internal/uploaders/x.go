package uploaders

import (
	"context"
	"fmt"
	"os"

	"github.com/dghubble/oauth1"
)

// XUploader handles X (Twitter) uploads
type XUploader struct {
	consumerKey       string
	consumerSecret    string
	accessToken       string
	accessTokenSecret string
}

// NewXUploader creates a new X uploader
func NewXUploader(consumerKey, consumerSecret, accessToken, accessTokenSecret string) *XUploader {
	return &XUploader{
		consumerKey:       consumerKey,
		consumerSecret:    consumerSecret,
		accessToken:       accessToken,
		accessTokenSecret: accessTokenSecret,
	}
}

// Platform returns the platform name
func (x *XUploader) Platform() string {
	return "x"
}

// Upload uploads a video to X (Twitter)
func (x *XUploader) Upload(ctx context.Context, req *UploadRequest) (*UploadResult, error) {
	if x.consumerKey == "" || x.consumerSecret == "" || x.accessToken == "" || x.accessTokenSecret == "" {
		return &UploadResult{
			Success:  false,
			Platform: "x",
			Error:    "Missing credentials",
			Details:  map[string]string{"error": "X credentials not found"},
		}, fmt.Errorf("X credentials not set")
	}

	// Remove #shorts hashtag from text
	text := RemoveShortsHashtag(req.Caption)
	if text == "" {
		text = req.Title
	}

	// Create OAuth1 config
	config := oauth1.NewConfig(x.consumerKey, x.consumerSecret)
	token := oauth1.NewToken(x.accessToken, x.accessTokenSecret)
	_ = config.Client(ctx, token)

	// For now, we'll just return a stub implementation
	// Full implementation would require:
	// 1. Upload media using v1.1 API (media/upload)
	// 2. Create tweet with media_ids using v2 API
	// This matches the Python implementation structure

	return &UploadResult{
		Success:  false,
		Platform: "x",
		Error:    "Not implemented",
		Details: map[string]string{
			"error": "X upload not fully implemented in Go version yet",
			"text":  text,
		},
	}, fmt.Errorf("X upload not implemented")
}

// Helper function to read file
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
