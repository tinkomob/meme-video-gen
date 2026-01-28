package uploaders

import (
	"context"
	"fmt"
	"os"
)

// Manager manages all uploaders
type Manager struct {
	uploaders map[string]Uploader
}

// NewManager creates a new uploader manager
func NewManager() *Manager {
	m := &Manager{
		uploaders: make(map[string]Uploader),
	}

	// Initialize YouTube uploader
	if credPath := os.Getenv("CLIENT_SECRETS"); credPath != "" {
		tokenPath := os.Getenv("TOKEN_PICKLE")
		if tokenPath == "" {
			tokenPath = "token.pickle"
		}
		m.uploaders["youtube"] = NewYouTubeUploader(credPath, tokenPath)
	}

	// Initialize Telegram uploader
	if botToken := os.Getenv("TELEGRAM_BOT_TOKEN"); botToken != "" {
		chatID := os.Getenv("POSTS_CHAT_ID")
		m.uploaders["telegram"] = NewTelegramUploader(botToken, chatID)
	}

	// Initialize Instagram uploader (sends to Telegram POSTS_CHAT_ID)
	if botToken := os.Getenv("TELEGRAM_BOT_TOKEN"); botToken != "" {
		if chatIDStr := os.Getenv("POSTS_CHAT_ID"); chatIDStr != "" {
			var chatID int64
			fmt.Sscanf(chatIDStr, "%d", &chatID)
			if chatID != 0 {
				m.uploaders["instagram"] = NewInstagramUploader(botToken, chatID)
			}
		}
	}

	// Initialize X uploader
	consumerKey := os.Getenv("X_CONSUMER_KEY")
	consumerSecret := os.Getenv("X_CONSUMER_SECRET")
	accessToken := os.Getenv("X_ACCESS_TOKEN")
	accessTokenSecret := os.Getenv("X_ACCESS_TOKEN_SECRET")
	if consumerKey != "" && consumerSecret != "" && accessToken != "" && accessTokenSecret != "" {
		m.uploaders["x"] = NewXUploader(consumerKey, consumerSecret, accessToken, accessTokenSecret)
	}

	return m
}

// GetUploader returns an uploader for the specified platform
func (m *Manager) GetUploader(platform string) (Uploader, error) {
	uploader, ok := m.uploaders[platform]
	if !ok {
		return nil, fmt.Errorf("uploader not found for platform: %s", platform)
	}
	return uploader, nil
}

// Upload uploads to the specified platform
func (m *Manager) Upload(ctx context.Context, platform string, req *UploadRequest) (*UploadResult, error) {
	uploader, err := m.GetUploader(platform)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: platform,
			Error:    err.Error(),
		}, err
	}

	return uploader.Upload(ctx, req)
}

// UploadToAll uploads to all configured platforms
func (m *Manager) UploadToAll(ctx context.Context, req *UploadRequest) map[string]*UploadResult {
	results := make(map[string]*UploadResult)

	for platform := range m.uploaders {
		result, _ := m.Upload(ctx, platform, req)
		results[platform] = result
	}

	return results
}

// UploadToSelected uploads to selected platforms
func (m *Manager) UploadToSelected(ctx context.Context, platforms []string, req *UploadRequest) map[string]*UploadResult {
	results := make(map[string]*UploadResult)

	for _, platform := range platforms {
		result, _ := m.Upload(ctx, platform, req)
		results[platform] = result
	}

	return results
}

// AvailablePlatforms returns list of available platforms
func (m *Manager) AvailablePlatforms() []string {
	platforms := make([]string, 0, len(m.uploaders))
	for platform := range m.uploaders {
		platforms = append(platforms, platform)
	}
	return platforms
}
