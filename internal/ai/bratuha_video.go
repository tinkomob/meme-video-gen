package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"meme-video-gen/internal/logging"
)

type BratuhaVideoClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	log        *logging.Logger
}

type bratuhaCreateOperationRequest struct {
	Tool  string                 `json:"tool"`
	Input map[string]interface{} `json:"input"`
}

type bratuhaCreateOperationResponse struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	Tool         string  `json:"tool"`
	Cost         int     `json:"cost"`
	BalanceAfter float64 `json:"balance_after"`
	CreatedAt    string  `json:"created_at"`
	Error        *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type bratuhaOperationResult struct {
	Type   string   `json:"type"`
	URLs   []string `json:"urls"`
	Images []string `json:"images"`
}

type bratuhaOperationResponse struct {
	ID           string                  `json:"id"`
	Status       string                  `json:"status"`
	Tool         string                  `json:"tool"`
	Cost         int                     `json:"cost"`
	CreatedAt    string                  `json:"created_at"`
	CompletedAt  string                  `json:"completed_at"`
	Result       *bratuhaOperationResult `json:"result"`
	ErrorMessage string                  `json:"error_message"`
	Error        *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func NewBratuhaVideoClient(apiKey string, log *logging.Logger) *BratuhaVideoClient {
	if apiKey == "" {
		return &BratuhaVideoClient{apiKey: "", baseURL: "https://bratuha.ru/api/v1", httpClient: &http.Client{Timeout: 30 * time.Second}, log: log}
	}
	return &BratuhaVideoClient{
		apiKey:     apiKey,
		baseURL:    "https://bratuha.ru/api/v1",
		httpClient: &http.Client{Timeout: 30 * time.Second},
		log:        log,
	}
}

func (c *BratuhaVideoClient) GenerateVideoURL(ctx context.Context, prompt string) (string, string, error) {
	if c.apiKey == "" {
		return "", "", fmt.Errorf("BRATUHA_API_KEY is not configured")
	}

	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", "", fmt.Errorf("video prompt is empty")
	}
	prompt = truncateString(prompt, 5000)

	operationID, err := c.createOperation(ctx, prompt)
	if err != nil {
		return "", "", err
	}

	videoURL, err := c.waitForVideoURL(ctx, operationID)
	if err != nil {
		return "", operationID, err
	}

	return videoURL, operationID, nil
}

func (c *BratuhaVideoClient) createOperation(ctx context.Context, prompt string) (string, error) {
	requestBody := bratuhaCreateOperationRequest{
		Tool: "grok-video",
		Input: map[string]interface{}{
			"model":        "grok-imagine/text-to-video",
			"prompt":       prompt,
			"aspect_ratio": "9:16",
			"duration":     "12",
			"resolution":   "480p",
			"mode":         "normal",
		},
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("marshal bratuha request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/operations", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create bratuha request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bratuha create operation: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read bratuha create response: %w", err)
	}

	var parsed bratuhaCreateOperationResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode bratuha create response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", c.formatAPIError("create operation", resp.StatusCode, body, parsed.Error)
	}

	if parsed.ID == "" {
		return "", fmt.Errorf("bratuha create operation: empty operation id")
	}

	if c.log != nil {
		c.log.Infof("bratuha: operation created id=%s status=%s", parsed.ID, parsed.Status)
	}

	return parsed.ID, nil
}

func (c *BratuhaVideoClient) waitForVideoURL(ctx context.Context, operationID string) (string, error) {
	deadline := time.NewTimer(15 * time.Minute)
	defer deadline.Stop()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		result, err := c.getOperation(ctx, operationID)
		if err != nil {
			return "", err
		}

		switch strings.ToLower(result.Status) {
		case "completed":
			if result.Result == nil || len(result.Result.URLs) == 0 {
				return "", fmt.Errorf("bratuha operation %s completed without video url", operationID)
			}
			return result.Result.URLs[0], nil
		case "failed":
			if result.ErrorMessage != "" {
				return "", fmt.Errorf("bratuha operation %s failed: %s", operationID, result.ErrorMessage)
			}
			return "", fmt.Errorf("bratuha operation %s failed", operationID)
		case "queued", "processing", "created", "pending":
			if c.log != nil {
				c.log.Infof("bratuha: operation %s status=%s", operationID, result.Status)
			}
		default:
			if result.Status != "" {
				if c.log != nil {
					c.log.Infof("bratuha: operation %s unexpected status=%s", operationID, result.Status)
				}
			}
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("bratuha operation %s timed out waiting for video", operationID)
		case <-ticker.C:
		}
	}
}

func (c *BratuhaVideoClient) getOperation(ctx context.Context, operationID string) (*bratuhaOperationResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/operations/"+operationID, nil)
	if err != nil {
		return nil, fmt.Errorf("create bratuha status request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bratuha get operation: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read bratuha status response: %w", err)
	}

	var parsed bratuhaOperationResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode bratuha status response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, c.formatAPIError("get operation", resp.StatusCode, body, parsed.Error)
	}

	return &parsed, nil
}

func (c *BratuhaVideoClient) formatAPIError(action string, statusCode int, body []byte, apiError *struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}) error {
	if apiError != nil {
		if apiError.Code != "" || apiError.Message != "" {
			if apiError.Code != "" {
				return fmt.Errorf("bratuha %s failed: %s: %s (http %d)", action, apiError.Code, apiError.Message, statusCode)
			}
			return fmt.Errorf("bratuha %s failed: %s (http %d)", action, apiError.Message, statusCode)
		}
	}

	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return fmt.Errorf("bratuha %s failed with http %d", action, statusCode)
	}
	return fmt.Errorf("bratuha %s failed with http %d: %s", action, statusCode, trimmed)
}
