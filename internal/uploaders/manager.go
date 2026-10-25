package uploaders

import (
	"context"
	"fmt"
	"os"
	"sync"
)

// Manager manages all uploaders
type Manager struct {
	mu        sync.RWMutex
	uploaders map[string]Uploader
}

// NewManager creates a new uploader manager
func NewManager() *Manager {
	m := &Manager{
		uploaders: make(map[string]Uploader),
	}

	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatIDStr := os.Getenv("POSTS_CHATID") // Load from env directly (not POSTS_CHAT_ID)
	if chatIDStr == "" {
		chatIDStr = os.Getenv("POSTS_CHAT_ID") // Fallback to POSTS_CHAT_ID
	}

	// YouTube uploader will be initialized on-demand from S3 via InitializeYouTubeUploaderFromS3
	// (not initialized here because credentials are on S3, not in env vars or local files)

	// Initialize Telegram uploader
	if botToken != "" && chatIDStr != "" {
		m.uploaders["telegram"] = NewTelegramUploader(botToken, chatIDStr)
	}

	// Note: Instagram is removed to avoid duplicate uploads (Telegram already handles it)
	// If you want Instagram uploads separately, implement proper Instagram API integration

	return m
}

// GetUploader returns an uploader for the specified platform
func (m *Manager) GetUploader(platform string) (Uploader, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
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

	for _, platform := range m.AvailablePlatforms() {
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	platforms := make([]string, 0, len(m.uploaders))
	for platform := range m.uploaders {
		platforms = append(platforms, platform)
	}
	return platforms
}

// UpdateTelegramChatID updates the chat ID for Telegram uploader
func (m *Manager) UpdateTelegramChatID(chatID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if uploader, ok := m.uploaders["telegram"]; ok {
		if tgUploader, ok := uploader.(*TelegramUploader); ok {
			m.uploaders["telegram"] = NewTelegramUploader(tgUploader.botToken, chatID)
		}
	}
}

// AddUploader adds or replaces an uploader for a platform
func (m *Manager) AddUploader(platform string, uploader Uploader) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.uploaders[platform] = uploader
}
