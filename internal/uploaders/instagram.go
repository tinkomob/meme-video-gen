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
	"strings"
	"time"
)

// InstagramUploader handles Instagram Reels uploads via upload-post.com API
type InstagramUploader struct {
	apiKey   string
	username string
}

// NewInstagramUploader creates a new Instagram uploader
func NewInstagramUploader(apiKey, username string) *InstagramUploader {
	return &InstagramUploader{
		apiKey:   apiKey,
		username: username,
	}
}

// Platform returns the platform name
func (i *InstagramUploader) Platform() string {
	return "instagram"
}

// Upload uploads a video to Instagram
func (i *InstagramUploader) Upload(ctx context.Context, req *UploadRequest) (*UploadResult, error) {
	if i.apiKey == "" {
		return &UploadResult{
			Success:  false,
			Platform: "instagram",
			Error:    "Missing API key",
			Details:  map[string]string{"error": "UPLOAD_POST_API_KEY required"},
		}, fmt.Errorf("UPLOAD_POST_API_KEY not set")
	}

	if i.username == "" {
		return &UploadResult{
			Success:  false,
			Platform: "instagram",
			Error:    "Missing username",
			Details:  map[string]string{"error": "INSTAGRAM_USERNAME required"},
		}, fmt.Errorf("INSTAGRAM_USERNAME not set")
	}

	// Open video file
	videoFile, err := os.Open(req.VideoPath)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "instagram",
			Error:    fmt.Sprintf("Failed to open video: %v", err),
		}, err
	}
	defer videoFile.Close()

	// Extract song name from caption
	songName := "Unknown"
	if req.Caption != "" && strings.Contains(req.Caption, "♪") {
		parts := strings.Split(req.Caption, "♪")
		if len(parts) >= 2 {
			songName = strings.TrimSpace(parts[1])
		}
	} else if req.Title != "" {
		songName = req.Title
	}

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add video file
	part, err := writer.CreateFormFile("video", "video.mp4")
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "instagram",
			Error:    fmt.Sprintf("Failed to create form file: %v", err),
		}, err
	}

	_, err = io.Copy(part, videoFile)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "instagram",
			Error:    fmt.Sprintf("Failed to copy video data: %v", err),
		}, err
	}

	// Add form fields
	_ = writer.WriteField("user", i.username)
	_ = writer.WriteField("title", songName)
	_ = writer.WriteField("platform[]", "instagram")
	_ = writer.WriteField("async_upload", "true")
	_ = writer.WriteField("media_type", "REELS")
	_ = writer.WriteField("share_to_feed", "true")
	_ = writer.WriteField("first_comment", fmt.Sprintf("song is %s", songName))

	err = writer.Close()
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "instagram",
			Error:    fmt.Sprintf("Failed to close writer: %v", err),
		}, err
	}

	// Send request
	client := &http.Client{
		Timeout: 300 * time.Second, // 5 minutes
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.upload-post.com/api/upload", body)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "instagram",
			Error:    fmt.Sprintf("Failed to create request: %v", err),
		}, err
	}

	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	httpReq.Header.Set("Authorization", fmt.Sprintf("Apikey %s", i.apiKey))

	resp, err := client.Do(httpReq)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "instagram",
			Error:    fmt.Sprintf("Request failed: %v", err),
		}, err
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "instagram",
			Error:    fmt.Sprintf("Failed to read response: %v", err),
		}, err
	}

	if resp.StatusCode != 200 {
		var errMsg string
		var errData map[string]interface{}
		if err := json.Unmarshal(respBody, &errData); err == nil {
			if msg, ok := errData["message"].(string); ok {
				errMsg = msg
			}
		}
		if errMsg == "" {
			errMsg = string(respBody)
			if len(errMsg) > 200 {
				errMsg = errMsg[:200]
			}
		}

		return &UploadResult{
			Success:  false,
			Platform: "instagram",
			Error:    "Upload failed",
			Details:  map[string]string{"error": errMsg},
		}, fmt.Errorf("instagram upload failed: %s", errMsg)
	}

	// Parse success response
	var result map[string]interface{}
	err = json.Unmarshal(respBody, &result)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "instagram",
			Error:    fmt.Sprintf("Failed to parse response: %v", err),
		}, err
	}

	return &UploadResult{
		Success:  true,
		Platform: "instagram",
		Details: map[string]string{
			"message": "Video uploaded successfully (async)",
			"song":    songName,
		},
	}, nil
}
