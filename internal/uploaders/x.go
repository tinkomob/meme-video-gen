package uploaders

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
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
	initURL := "https://upload.twitter.com/1.1/media/upload.json"

	// Use form data as per API docs
	initData := url.Values{}
	initData.Set("command", "INIT")
	initData.Set("total_bytes", strconv.Itoa(len(fileData)))
	initData.Set("media_type", "video/mp4")
	initData.Set("media_category", "tweet_video")

	initReq, _ := http.NewRequestWithContext(ctx, "POST", initURL, strings.NewReader(initData.Encode()))
	initReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	initResp, err := x.httpClient.Do(initReq)
	if err != nil {
		return "", fmt.Errorf("failed to initialize media upload: %w", err)
	}
	defer initResp.Body.Close()

	if initResp.StatusCode != http.StatusOK && initResp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(initResp.Body)
		bodyStr := string(bodyBytes)
		return "", fmt.Errorf("INIT request failed with status %d: %s", initResp.StatusCode, bodyStr)
	}

	var initRes mediaUploadResponse
	if err := json.NewDecoder(initResp.Body).Decode(&initRes); err != nil {
		return "", fmt.Errorf("failed to parse INIT response: %w", err)
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

		appendURL := "https://upload.twitter.com/1.1/media/upload.json"

		// Create multipart form
		var appendBody bytes.Buffer
		writer := multipart.NewWriter(&appendBody)

		writer.WriteField("command", "APPEND")
		writer.WriteField("media_id", mediaID)
		writer.WriteField("segment_index", strconv.Itoa(i/chunkSize))

		mediaField, _ := writer.CreateFormFile("media", "chunk.bin")
		mediaField.Write(chunk)

		writer.Close()

		appendReq, _ := http.NewRequestWithContext(ctx, "POST", appendURL, &appendBody)
		appendReq.Header.Set("Content-Type", writer.FormDataContentType())

		appendResp, err := x.httpClient.Do(appendReq)
		if err != nil {
			return "", fmt.Errorf("failed to append media chunk: %w", err)
		}
		defer appendResp.Body.Close()

		if appendResp.StatusCode != http.StatusNoContent && appendResp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(appendResp.Body)
			return "", fmt.Errorf("APPEND request failed with status %d: %s", appendResp.StatusCode, string(bodyBytes))
		}
	}

	// Step 3: FINALIZE - Complete upload
	finalizeURL := "https://upload.twitter.com/1.1/media/upload.json"

	finalizeData := url.Values{}
	finalizeData.Set("command", "FINALIZE")
	finalizeData.Set("media_id", mediaID)

	finalizeReq, _ := http.NewRequestWithContext(ctx, "POST", finalizeURL, strings.NewReader(finalizeData.Encode()))
	finalizeReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	finalizeResp, err := x.httpClient.Do(finalizeReq)
	if err != nil {
		return "", fmt.Errorf("failed to finalize media upload: %w", err)
	}
	defer finalizeResp.Body.Close()

	if finalizeResp.StatusCode != http.StatusOK && finalizeResp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(finalizeResp.Body)
		bodyStr := string(bodyBytes)
		return "", fmt.Errorf("FINALIZE request failed with status %d: %s", finalizeResp.StatusCode, bodyStr)
	}

	var finalizeRes mediaUploadResponse
	if err := json.NewDecoder(finalizeResp.Body).Decode(&finalizeRes); err != nil {
		return "", fmt.Errorf("failed to parse FINALIZE response: %w", err)
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
	
	// Log successful media upload
	fmt.Printf("[DEBUG] X: Media uploaded successfully, mediaID=%s, text=%s\n", mediaID, text)

	// Step 2: Create post with media using v2 API
	postURL := "https://api.x.com/2/tweets"

	postBody := map[string]interface{}{
		"text": text,
		"media": map[string]interface{}{
			"media_ids": []string{mediaID},
		},
	}

	postBodyJSON, _ := json.Marshal(postBody)
	fmt.Printf("[DEBUG] X: Creating post with body: %s\n", string(postBodyJSON))
	
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

	// Read and attempt to parse response
	bodyBytes, _ := io.ReadAll(postResp.Body)
	bodyLen := len(bodyBytes)
	if bodyLen > 200 {
		bodyLen = 200
	}
	fmt.Printf("[DEBUG] X: Response status=%d, body=%s\n", postResp.StatusCode, string(bodyBytes[:bodyLen]))

	var postRes createPostResponse
	if err := json.Unmarshal(bodyBytes, &postRes); err != nil {
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
		errorMsg := fmt.Sprintf("status=%d", postResp.StatusCode)
		
		// Try to extract error details from API response
		if len(postRes.Errors) > 0 {
			apiErr := postRes.Errors[0]
			if apiErr.Detail != "" {
				errorMsg = fmt.Sprintf("%s | detail=%s", errorMsg, apiErr.Detail)
			}
			if apiErr.Title != "" {
				errorMsg = fmt.Sprintf("%s | title=%s", errorMsg, apiErr.Title)
			}
			if apiErr.Type != "" {
				errorMsg = fmt.Sprintf("%s | type=%s", errorMsg, apiErr.Type)
			}
		}

		// If error message is still minimal, try to read response body for more info
		if errorMsg == fmt.Sprintf("status=%d", postResp.StatusCode) {
			if len(bodyBytes) > 0 {
				bodyLen := len(bodyBytes)
				if bodyLen > 500 {
					bodyLen = 500
				}
				errorMsg = fmt.Sprintf("%s | body=%s", errorMsg, string(bodyBytes[:bodyLen]))
			}
		}

		return &UploadResult{
			Success:  false,
			Platform: "x",
			Error:    "Post creation failed",
			Details: map[string]string{
				"error":  errorMsg,
				"text":   text,
				"status": fmt.Sprintf("%d", postResp.StatusCode),
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
