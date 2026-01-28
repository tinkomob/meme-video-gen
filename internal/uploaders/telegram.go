package uploaders

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"time"
)

// TelegramUploader handles Telegram channel posts
type TelegramUploader struct {
	botToken string
	chatID   string
}

// NewTelegramUploader creates a new Telegram uploader
func NewTelegramUploader(botToken, chatID string) *TelegramUploader {
	return &TelegramUploader{
		botToken: botToken,
		chatID:   chatID,
	}
}

// SetChatID updates the chat ID (for dynamic loading from S3)
func (t *TelegramUploader) SetChatID(chatID string) {
	t.chatID = chatID
}

// Platform returns the platform name
func (t *TelegramUploader) Platform() string {
	return "telegram"
}

// Upload uploads a video to Telegram channel
func (t *TelegramUploader) Upload(ctx context.Context, req *UploadRequest) (*UploadResult, error) {
	if t.botToken == "" {
		return &UploadResult{
			Success:  false,
			Platform: "telegram",
			Error:    "Missing bot token",
			Details:  map[string]string{"error": "TELEGRAM_BOT_TOKEN required"},
		}, fmt.Errorf("TELEGRAM_BOT_TOKEN not set")
	}

	if t.chatID == "" {
		return &UploadResult{
			Success:  false,
			Platform: "telegram",
			Error:    "Missing chat ID",
			Details:  map[string]string{"error": "POSTS_CHAT_ID required"},
		}, fmt.Errorf("POSTS_CHAT_ID not set")
	}

	// Open video file
	videoFile, err := os.Open(req.VideoPath)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "telegram",
			Error:    fmt.Sprintf("Failed to open video: %v", err),
		}, err
	}
	defer videoFile.Close()

	// Prepare caption
	caption := req.Caption
	if caption == "" && req.Title != "" {
		caption = fmt.Sprintf("song is %s", req.Title)
	}

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add video file
	part, err := writer.CreateFormFile("video", "video.mp4")
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "telegram",
			Error:    fmt.Sprintf("Failed to create form file: %v", err),
		}, err
	}

	_, err = io.Copy(part, videoFile)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "telegram",
			Error:    fmt.Sprintf("Failed to copy video data: %v", err),
		}, err
	}

	// Add form fields
	_ = writer.WriteField("chat_id", t.chatID)
	_ = writer.WriteField("caption", caption)
	_ = writer.WriteField("parse_mode", "HTML")

	err = writer.Close()
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "telegram",
			Error:    fmt.Sprintf("Failed to close writer: %v", err),
		}, err
	}

	// Send request
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendVideo", t.botToken)

	client := &http.Client{
		Timeout: 300 * time.Second, // 5 minutes
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "telegram",
			Error:    fmt.Sprintf("Failed to create request: %v", err),
		}, err
	}

	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(httpReq)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "telegram",
			Error:    fmt.Sprintf("Request failed: %v", err),
		}, err
	}
	defer resp.Body.Close()

	// Parse response
	var result struct {
		Ok          bool        `json:"ok"`
		Description string      `json:"description"`
		Error       string      `json:"error"`
		ErrorCode   int         `json:"error_code"`
		Result      interface{} `json:"result"`
	}

	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "telegram",
			Error:    fmt.Sprintf("Failed to parse response: %v", err),
		}, err
	}

	if !result.Ok {
		errMsg := result.Description
		if errMsg == "" {
			errMsg = result.Error
		}
		if result.ErrorCode != 0 {
			errMsg = fmt.Sprintf("Error %d: %s", result.ErrorCode, errMsg)
		}
		return &UploadResult{
			Success:  false,
			Platform: "telegram",
			Error:    errMsg,
			Details:  map[string]string{"chat_id": t.chatID, "error": errMsg},
		}, fmt.Errorf("telegram post failed: %s", errMsg)
	}

	return &UploadResult{
		Success:  true,
		Platform: "telegram",
		Details: map[string]string{
			"status": "video sent to channel",
		},
	}, nil
}
