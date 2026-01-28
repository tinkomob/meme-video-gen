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
	"regexp"
	"strconv"
	"strings"

	"github.com/dghubble/oauth1"
)

// XUploader handles X (Twitter) uploads
type XUploader struct {
	consumerKey       string
	consumerSecret    string
	accessToken       string
	accessTokenSecret string
	httpClient        *http.Client
}

// NewXUploader creates a new X uploader
func NewXUploader(consumerKey, consumerSecret, accessToken, accessTokenSecret string) *XUploader {
	// Create OAuth1 config and client
	config := oauth1.NewConfig(consumerKey, consumerSecret)
	token := oauth1.NewToken(accessToken, accessTokenSecret)
	httpClient := config.Client(context.Background(), token)

	return &XUploader{
		consumerKey:       consumerKey,
		consumerSecret:    consumerSecret,
		accessToken:       accessToken,
		accessTokenSecret: accessTokenSecret,
		httpClient:        httpClient,
	}
}

// Platform returns the platform name
func (x *XUploader) Platform() string {
	return "x"
}

// mediaUploadResponse is the response from v1.1 media/upload endpoint
type mediaUploadResponse struct {
	MediaID      int64  `json:"media_id"`
	MediaIDStr   string `json:"media_id_str"`
	MediaKey     string `json:"media_key"`
	Size         int    `json:"size"`
	ExpiresAfter int    `json:"expires_after_secs"`
	Image        struct {
		ImageType string `json:"image_type"`
		W         int    `json:"w"`
		H         int    `json:"h"`
	} `json:"image"`
	Video struct {
		VideoType string `json:"video_type"`
	} `json:"video"`
}

// uploadMedia uploads a video to X v1.1 media/upload endpoint
func (x *XUploader) uploadMedia(ctx context.Context, videoPath string) (string, error) {
	// Read video file
	file, err := os.Open(videoPath)
	if err != nil {
		return "", fmt.Errorf("failed to open video file: %w", err)
	}
	defer file.Close()

	// Read entire file for chunked upload
	fileData, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("failed to read video file: %w", err)
	}

	// Step 1: INIT - Initialize chunked upload
	initURL := "https://upload.x.com/1.1/media/upload.json"

	fileInfo, err := os.Stat(videoPath)
	if err != nil {
		return "", fmt.Errorf("failed to get file size: %w", err)
	}

	initBody := map[string]interface{}{
		"command":        "INIT",
		"total_bytes":    fileInfo.Size(),
		"media_type":     "video/mp4",
		"media_category": "tweet_video",
	}

	initBodyJSON, _ := json.Marshal(initBody)
	initReq, _ := http.NewRequestWithContext(ctx, "POST", initURL, bytes.NewBuffer(initBodyJSON))
	initReq.Header.Set("Content-Type", "application/json")

	initResp, err := x.httpClient.Do(initReq)
	if err != nil {
		return "", fmt.Errorf("failed to initialize media upload: %w", err)
	}
	defer initResp.Body.Close()

	var initRes mediaUploadResponse
	if err := json.NewDecoder(initResp.Body).Decode(&initRes); err != nil {
		return "", fmt.Errorf("failed to parse INIT response: %w", err)
	}

	if initResp.StatusCode != http.StatusOK && initResp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("INIT request failed with status %d", initResp.StatusCode)
	}

	mediaID := strconv.FormatInt(initRes.MediaID, 10)

	// Step 2: APPEND - Upload video chunks
	const chunkSize = 5 * 1024 * 1024 // 5MB chunks
	for i := 0; i < len(fileData); i += chunkSize {
		end := i + chunkSize
		if end > len(fileData) {
			end = len(fileData)
		}

		chunk := fileData[i:end]

		appendURL := "https://upload.x.com/1.1/media/upload.json"

		// Create multipart form
		var appendBody bytes.Buffer
		writer := multipart.NewWriter(&appendBody)

		writer.WriteField("command", "APPEND")
		writer.WriteField("media_id", mediaID)
		writer.WriteField("segment_index", strconv.Itoa(i/chunkSize))

		mediaField, _ := writer.CreateFormFile("media_data", "chunk.bin")
		mediaField.Write(chunk)

		writer.Close()

		appendReq, _ := http.NewRequestWithContext(ctx, "POST", appendURL, &appendBody)
		appendReq.Header.Set("Content-Type", writer.FormDataContentType())

		appendResp, err := x.httpClient.Do(appendReq)
		if err != nil {
			return "", fmt.Errorf("failed to append media chunk: %w", err)
		}
		appendResp.Body.Close()

		if appendResp.StatusCode != http.StatusNoContent && appendResp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("APPEND request failed with status %d", appendResp.StatusCode)
		}
	}

	// Step 3: FINALIZE - Complete upload
	finalizeURL := "https://upload.x.com/1.1/media/upload.json"

	finalizeBody := map[string]interface{}{
		"command":  "FINALIZE",
		"media_id": mediaID,
	}

	finalizeBodyJSON, _ := json.Marshal(finalizeBody)
	finalizeReq, _ := http.NewRequestWithContext(ctx, "POST", finalizeURL, bytes.NewBuffer(finalizeBodyJSON))
	finalizeReq.Header.Set("Content-Type", "application/json")

	finalizeResp, err := x.httpClient.Do(finalizeReq)
	if err != nil {
		return "", fmt.Errorf("failed to finalize media upload: %w", err)
	}
	defer finalizeResp.Body.Close()

	var finalizeRes mediaUploadResponse
	if err := json.NewDecoder(finalizeResp.Body).Decode(&finalizeRes); err != nil {
		return "", fmt.Errorf("failed to parse FINALIZE response: %w", err)
	}

	if finalizeResp.StatusCode != http.StatusOK && finalizeResp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("FINALIZE request failed with status %d", finalizeResp.StatusCode)
	}

	return mediaID, nil
}

// createPostResponse is the response from v2 tweets endpoint
type createPostResponse struct {
	Data struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	} `json:"data"`
	Errors []struct {
		Title  string `json:"title"`
		Type   string `json:"type"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	} `json:"errors"`
}

// Upload uploads a video to X (Twitter) using v2 API
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

	// Step 1: Upload video file to v1.1 media endpoint
	mediaID, err := x.uploadMedia(ctx, req.VideoPath)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "x",
			Error:    "Media upload failed",
			Details: map[string]string{
				"error": err.Error(),
				"text":  text,
			},
		}, fmt.Errorf("failed to upload media: %w", err)
	}

	// Step 2: Create post with media using v2 API
	postURL := "https://api.x.com/2/tweets"

	postBody := map[string]interface{}{
		"text": text,
		"media": map[string]interface{}{
			"media_ids": []string{mediaID},
		},
	}

	postBodyJSON, _ := json.Marshal(postBody)
	postReq, _ := http.NewRequestWithContext(ctx, "POST", postURL, bytes.NewBuffer(postBodyJSON))
	postReq.Header.Set("Content-Type", "application/json")

	postResp, err := x.httpClient.Do(postReq)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "x",
			Error:    "Post creation failed",
			Details: map[string]string{
				"error": err.Error(),
				"text":  text,
			},
		}, fmt.Errorf("failed to create post: %w", err)
	}
	defer postResp.Body.Close()

	var postRes createPostResponse
	if err := json.NewDecoder(postResp.Body).Decode(&postRes); err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "x",
			Error:    "Failed to parse response",
			Details: map[string]string{
				"error": err.Error(),
				"text":  text,
			},
		}, fmt.Errorf("failed to parse post response: %w", err)
	}

	if postResp.StatusCode != http.StatusCreated {
		errorMsg := fmt.Sprintf("API returned status %d", postResp.StatusCode)
		if len(postRes.Errors) > 0 {
			errorMsg = postRes.Errors[0].Detail
		}

		return &UploadResult{
			Success:  false,
			Platform: "x",
			Error:    "Post creation failed",
			Details: map[string]string{
				"error": errorMsg,
				"text":  text,
			},
		}, fmt.Errorf("post creation failed: %s", errorMsg)
	}

	// Construct post URL
	postURL = fmt.Sprintf("https://x.com/i/web/status/%s", postRes.Data.ID)

	return &UploadResult{
		Success:  true,
		Platform: "x",
		URL:      postURL,
		Details: map[string]string{
			"tweet_id": postRes.Data.ID,
			"text":     text,
		},
	}, nil
}

// RemoveShortsHashtag removes #shorts hashtag from text
func RemoveShortsHashtag(s string) string {
	if s == "" {
		return s
	}
	// Remove #shorts case-insensitively
	re := regexp.MustCompile(`(?i)(?:^|\s)#shorts\b`)
	result := re.ReplaceAllString(s, " ")
	// Clean up extra whitespace
	re = regexp.MustCompile(`\s{2,}`)
	result = re.ReplaceAllString(result, " ")
	return strings.TrimSpace(result)
}

// Helper function to read file
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
