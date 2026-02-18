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
	"time"

	"github.com/dghubble/oauth1"
)

// XUploader handles X API uploads
type XUploader struct {
	consumerKey       string
	consumerSecret    string
	accessToken       string
	accessTokenSecret string
	httpClient        *http.Client
}

// NewXUploader creates a new X uploader
func NewXUploader(consumerKey, consumerSecret, accessToken, accessTokenSecret string) *XUploader {
	config := oauth1.NewConfig(consumerKey, consumerSecret)
	token := oauth1.NewToken(accessToken, accessTokenSecret)

	return &XUploader{
		consumerKey:       consumerKey,
		consumerSecret:    consumerSecret,
		accessToken:       accessToken,
		accessTokenSecret: accessTokenSecret,
		httpClient:        config.Client(context.Background(), token),
	}
}

// Platform returns the platform name
func (x *XUploader) Platform() string {
	return "x"
}

// mediaInitResponse is the response from v2 media/upload initialize
type mediaInitResponse struct {
	Data struct {
		ID              string `json:"id"`
		MediaKey        string `json:"media_key"`
		ExpiresAfterSec int    `json:"expires_after_secs"`
		ProcessingInfo  *struct {
			State           string `json:"state"`
			CheckAfterSecs  int    `json:"check_after_secs"`
			ProgressPercent int    `json:"progress_percent"`
		} `json:"processing_info"`
	} `json:"data"`
	Errors []map[string]interface{} `json:"errors"`
}

// mediaStatusResponse is the response from v2 media/upload status check
type mediaStatusResponse struct {
	Data struct {
		ProcessingInfo struct {
			State           string `json:"state"`
			CheckAfterSecs  int    `json:"check_after_secs"`
			ProgressPercent int    `json:"progress_percent"`
		} `json:"processing_info"`
	} `json:"data"`
	Errors []map[string]interface{} `json:"errors"`
}

// postCreateResponse is the response from v2 tweets create
type postCreateResponse struct {
	Data struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	} `json:"data"`
	Errors []map[string]interface{} `json:"errors"`
}

// uploadMedia uploads a video using X API v2 chunked upload
func (x *XUploader) uploadMedia(ctx context.Context, videoPath string) (string, error) {
	file, err := os.Open(videoPath)
	if err != nil {
		return "", fmt.Errorf("failed to open video file: %w", err)
	}
	defer file.Close()

	fileData, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("failed to read video file: %w", err)
	}

	// Step 1: Initialize upload
	initURL := "https://api.x.com/2/media/upload/initialize"
	initBody := map[string]interface{}{
		"media_type":     "video/mp4",
		"total_bytes":    len(fileData),
		"media_category": "tweet_video",
	}

	initJSON, _ := json.Marshal(initBody)
	initReq, _ := http.NewRequestWithContext(ctx, "POST", initURL, bytes.NewBuffer(initJSON))
	initReq.Header.Set("Content-Type", "application/json")

	initResp, err := x.httpClient.Do(initReq)
	if err != nil {
		return "", fmt.Errorf("failed to initialize media upload: %w", err)
	}
	defer initResp.Body.Close()

	initBodyBytes, _ := io.ReadAll(initResp.Body)
	if initResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("INIT failed with status %d: %s", initResp.StatusCode, string(initBodyBytes))
	}

	var initRes mediaInitResponse
	if err := json.Unmarshal(initBodyBytes, &initRes); err != nil {
		return "", fmt.Errorf("failed to parse INIT response: %w", err)
	}

	mediaID := initRes.Data.ID

	// Step 2: Upload chunks
	const chunkSize = 5 * 1024 * 1024 // 5MB
	for i := 0; i < len(fileData); i += chunkSize {
		end := i + chunkSize
		if end > len(fileData) {
			end = len(fileData)
		}

		var appendBody bytes.Buffer
		writer := multipart.NewWriter(&appendBody)

		writer.WriteField("segment_index", strconv.Itoa(i/chunkSize))
		part, _ := writer.CreateFormFile("media", "video.mp4")
		part.Write(fileData[i:end])
		writer.Close()

		appendURL := fmt.Sprintf("https://api.x.com/2/media/upload/%s/append", mediaID)
		appendReq, _ := http.NewRequestWithContext(ctx, "POST", appendURL, &appendBody)
		appendReq.Header.Set("Content-Type", writer.FormDataContentType())

		appendResp, err := x.httpClient.Do(appendReq)
		if err != nil {
			return "", fmt.Errorf("failed to append media chunk: %w", err)
		}

		if appendResp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(appendResp.Body)
			appendResp.Body.Close()
			return "", fmt.Errorf("APPEND failed with status %d: %s", appendResp.StatusCode, string(bodyBytes))
		}
		appendResp.Body.Close()
	}

	// Step 3: Finalize upload
	finalizeURL := fmt.Sprintf("https://api.x.com/2/media/upload/%s/finalize", mediaID)
	finalizeReq, _ := http.NewRequestWithContext(ctx, "POST", finalizeURL, nil)

	finalizeResp, err := x.httpClient.Do(finalizeReq)
	if err != nil {
		return "", fmt.Errorf("failed to finalize media upload: %w", err)
	}
	defer finalizeResp.Body.Close()

	finalizeBodyBytes, _ := io.ReadAll(finalizeResp.Body)
	if finalizeResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("FINALIZE failed with status %d: %s", finalizeResp.StatusCode, string(finalizeBodyBytes))
	}

	var finalizeRes mediaInitResponse
	if err := json.Unmarshal(finalizeBodyBytes, &finalizeRes); err != nil {
		return "", fmt.Errorf("failed to parse FINALIZE response: %w", err)
	}

	// Step 4: Check processing status if needed
	if finalizeRes.Data.ProcessingInfo != nil {
		maxAttempts := 60
		for attempt := 0; attempt < maxAttempts; attempt++ {
			processingState := finalizeRes.Data.ProcessingInfo.State

			if processingState == "succeeded" {
				break
			}
			if processingState == "failed" {
				return "", fmt.Errorf("media processing failed")
			}

			// Wait before checking status again
			checkAfter := finalizeRes.Data.ProcessingInfo.CheckAfterSecs
			if checkAfter == 0 {
				checkAfter = 1
			}
			time.Sleep(time.Duration(checkAfter) * time.Second)

			// Check status
			statusURL := fmt.Sprintf("https://api.x.com/2/media/upload?command=STATUS&media_id=%s", mediaID)
			statusReq, _ := http.NewRequestWithContext(ctx, "GET", statusURL, nil)
			statusResp, err := x.httpClient.Do(statusReq)
			if err != nil {
				return "", fmt.Errorf("failed to check media status: %w", err)
			}

			statusBodyBytes, _ := io.ReadAll(statusResp.Body)
			statusResp.Body.Close()

			if statusResp.StatusCode != http.StatusOK {
				return "", fmt.Errorf("STATUS check failed with status %d: %s", statusResp.StatusCode, string(statusBodyBytes))
			}

			var statusRes mediaStatusResponse
			if err := json.Unmarshal(statusBodyBytes, &statusRes); err != nil {
				return "", fmt.Errorf("failed to parse STATUS response: %w", err)
			}

			finalizeRes.Data.ProcessingInfo = &struct {
				State           string `json:"state"`
				CheckAfterSecs  int    `json:"check_after_secs"`
				ProgressPercent int    `json:"progress_percent"`
			}{
				State:           statusRes.Data.ProcessingInfo.State,
				CheckAfterSecs:  statusRes.Data.ProcessingInfo.CheckAfterSecs,
				ProgressPercent: statusRes.Data.ProcessingInfo.ProgressPercent,
			}
		}
	}

	return mediaID, nil
}

// Upload uploads a video to X API
func (x *XUploader) Upload(ctx context.Context, req *UploadRequest) (*UploadResult, error) {
	if x.consumerKey == "" || x.consumerSecret == "" || x.accessToken == "" || x.accessTokenSecret == "" {
		return &UploadResult{
			Success:  false,
			Platform: "x",
			Error:    "Missing credentials",
			Details:  map[string]string{"error": "X credentials not found"},
		}, fmt.Errorf("X credentials not set")
	}

	text := RemoveShortsHashtag(req.Caption)
	if text == "" {
		text = req.Title
	}

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

	fmt.Printf("[DEBUG] X: Media uploaded successfully, mediaID=%s, text=%s\n", mediaID, text)

	// Create post with media
	postURL := "https://api.x.com/2/tweets"
	postBody := map[string]interface{}{
		"text": text,
		"media": map[string]interface{}{
			"media_ids": []string{mediaID},
		},
	}

	postJSON, _ := json.Marshal(postBody)
	fmt.Printf("[DEBUG] X: Creating post with body: %s\n", string(postJSON))

	postReq, _ := http.NewRequestWithContext(ctx, "POST", postURL, bytes.NewBuffer(postJSON))
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

	postBodyBytes, _ := io.ReadAll(postResp.Body)
	if len(postBodyBytes) > 200 {
		fmt.Printf("[DEBUG] X: Response status=%d, body=%s\n", postResp.StatusCode, string(postBodyBytes[:200]))
	} else {
		fmt.Printf("[DEBUG] X: Response status=%d, body=%s\n", postResp.StatusCode, string(postBodyBytes))
	}

	if postResp.StatusCode != http.StatusCreated {
		var postRes postCreateResponse
		json.Unmarshal(postBodyBytes, &postRes)

		errorMsg := fmt.Sprintf("status=%d", postResp.StatusCode)
		if len(postRes.Errors) > 0 {
			if errDetail, ok := postRes.Errors[0]["detail"].(string); ok {
				errorMsg += " | " + errDetail
			}
		} else if len(postBodyBytes) > 0 {
			if len(postBodyBytes) > 500 {
				errorMsg += " | " + string(postBodyBytes[:500])
			} else {
				errorMsg += " | " + string(postBodyBytes)
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

	var postRes postCreateResponse
	if err := json.Unmarshal(postBodyBytes, &postRes); err != nil {
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
	re := regexp.MustCompile(`(?i)(?:^|\s)#shorts\b`)
	result := re.ReplaceAllString(s, " ")
	re = regexp.MustCompile(`\s{2,}`)
	result = re.ReplaceAllString(result, " ")
	return strings.TrimSpace(result)
}

// Helper function to read file
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
