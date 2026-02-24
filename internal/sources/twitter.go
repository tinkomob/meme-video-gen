package sources

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"meme-video-gen/internal/model"
)

// TwitterUser represents a Twitter user
type TwitterUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// TwitterTweet represents a tweet with media
type TwitterTweet struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	AuthorID    string `json:"author_id"`
	Attachments *struct {
		MediaKeys []string `json:"media_keys"`
	} `json:"attachments"`
}

// TwitterMedia represents media from Twitter API
type TwitterMedia struct {
	MediaKey string `json:"media_key"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Variants []struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	} `json:"variants"`
	PublicMetrics *struct {
		ViewCount int `json:"view_count"`
	} `json:"public_metrics"`
}

// TwitterAPIResponse represents API response
type TwitterAPIResponse struct {
	Data     interface{}            `json:"data"`
	Includes *TwitterIncludes       `json:"includes"`
	Meta     map[string]interface{} `json:"meta"`
}

// TwitterIncludes represents included data (users, media)
type TwitterIncludes struct {
	Users []TwitterUser  `json:"users"`
	Media []TwitterMedia `json:"media"`
}

// scrapeTwitter extracts media from a Twitter profile URL.
// Implements the interface expected by scraper.go
func (sc *Scraper) scrapeTwitter(ctx context.Context, profileURL string) (*model.SourceAsset, error) {
	bearerToken := os.Getenv("X_BEARER_TOKEN")
	if bearerToken == "" {
		return nil, errors.New("X_BEARER_TOKEN not set. Get from https://developer.twitter.com/en/portal/dashboard")
	}

	username := parseTwitterUsername(profileURL)
	if username == "" {
		return nil, fmt.Errorf("invalid Twitter URL: %s", profileURL)
	}

	client := &http.Client{Timeout: 15 * time.Second}

	// Get user ID
	userID, err := sc.getTwitterUserID(ctx, client, bearerToken, username)
	if err != nil {
		if strings.Contains(err.Error(), "401") {
			return nil, fmt.Errorf("Twitter 401: X_BEARER_TOKEN is invalid/expired for @%s. Reset token at https://developer.twitter.com", username)
		}
		if strings.Contains(err.Error(), "429") {
			return nil, fmt.Errorf("Twitter rate-limited for @%s: wait before retry", username)
		}
		return nil, fmt.Errorf("get user ID for @%s: %w", username, err)
	}

	// Get recent tweets with media
	_, media, err := sc.getTwitterUserMedia(ctx, client, bearerToken, userID)
	if err != nil {
		return nil, fmt.Errorf("get media for @%s: %w", username, err)
	}

	if len(media) == 0 {
		return nil, fmt.Errorf("no media found for @%s", username)
	}

	// Try to download one of the media
	tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("twitter_%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return nil, err
	}

	for _, m := range media {
		if m.Type != "photo" {
			continue
		}

		mediaURL := m.URL
		if mediaURL == "" && len(m.Variants) > 0 {
			// Find best variant
			for _, v := range m.Variants {
				if strings.Contains(v.ContentType, "image") {
					mediaURL = v.URL
					break
				}
			}
		}

		if mediaURL == "" {
			continue
		}

		asset, err := sc.downloadTwitterMedia(ctx, client, mediaURL, username, tmpDir)
		if err == nil && asset != nil {
			return asset, nil
		}
	}

	return nil, fmt.Errorf("no media successfully downloaded from @%s", username)
}

// FetchFromTwitter fetches one image from Twitter sources
// This is an alternative interface that takes a list of sources
func (sc *Scraper) FetchFromTwitter(ctx context.Context, sources []string, outputDir string) (*model.SourceAsset, error) {
	if len(sources) == 0 {
		return nil, errors.New("twitter: no sources provided")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	bearerToken := os.Getenv("X_BEARER_TOKEN")
	if bearerToken == "" {
		return nil, errors.New("twitter: X_BEARER_TOKEN not configured")
	}

	client := &http.Client{Timeout: 15 * time.Second}

	// Shuffle sources
	shuffled := make([]string, len(sources))
	copy(shuffled, sources)
	for i := len(shuffled) - 1; i > 0; i-- {
		j, _ := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		k := int(j.Int64())
		shuffled[i], shuffled[k] = shuffled[k], shuffled[i]
	}

	for _, rawSource := range shuffled {
		rawSource = strings.TrimSpace(rawSource)
		if rawSource == "" {
			continue
		}

		username := parseTwitterUsername(rawSource)
		if username == "" {
			sc.logf("Twitter: Invalid source format: %s", rawSource)
			continue
		}

		sc.logf("Twitter: Fetching from @%s", username)

		// Get user ID
		userID, err := sc.getTwitterUserID(ctx, client, bearerToken, username)
		if err != nil {
			sc.logf("Twitter: Failed to get user ID for @%s: %v", username, err)
			continue
		}

		// Get recent tweets with media
		_, media, err := sc.getTwitterUserMedia(ctx, client, bearerToken, userID)
		if err != nil {
			sc.logf("Twitter: Failed to get media for @%s: %v", username, err)
			continue
		}

		if len(media) == 0 {
			sc.logf("Twitter: No media found for @%s", username)
			continue
		}

		// Try to download one of the media
		for _, m := range media {
			if m.Type != "photo" {
				continue
			}

			mediaURL := m.URL
			if mediaURL == "" && len(m.Variants) > 0 {
				// Find best variant
				for _, v := range m.Variants {
					if strings.Contains(v.ContentType, "image") {
						mediaURL = v.URL
						break
					}
				}
			}

			if mediaURL == "" {
				continue
			}

			asset, err := sc.downloadTwitterMedia(ctx, client, mediaURL, username, outputDir)
			if err == nil && asset != nil {
				return asset, nil
			}
			sc.logf("Twitter: Failed to download media from @%s: %v", username, err)
		}
	}

	sc.logf("Twitter: No media successfully downloaded from any source")
	return nil, errors.New("twitter: no media downloaded")
}

// parseTwitterUsername extracts username from various formats
func parseTwitterUsername(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Remove protocol and domain
	if strings.Contains(raw, "twitter.com") || strings.Contains(raw, "x.com") {
		parts := strings.Split(raw, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			part := strings.TrimSpace(parts[i])
			if part != "" && !strings.Contains(part, ".") && !strings.Contains(part, ":") {
				return part
			}
		}
	}

	// Remove @ prefix
	if strings.HasPrefix(raw, "@") {
		raw = raw[1:]
	}

	return strings.TrimSpace(raw)
}

// getTwitterUserID fetches user ID by username
func (sc *Scraper) getTwitterUserID(ctx context.Context, client *http.Client, bearerToken, username string) (string, error) {
	endpoint := "https://api.twitter.com/2/users/by/username/" + url.QueryEscape(username)

	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("User-Agent", "MemeVideoGen/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		reset := resp.Header.Get("x-rate-limit-reset")
		return "", fmt.Errorf("rate limited (reset: %s)", reset)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp TwitterAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", err
	}

	if dataMap, ok := apiResp.Data.(map[string]interface{}); ok {
		if id, ok := dataMap["id"].(string); ok {
			return id, nil
		}
	}

	return "", errors.New("user not found or invalid response")
}

// getTwitterUserMedia fetches recent media from user
func (sc *Scraper) getTwitterUserMedia(ctx context.Context, client *http.Client, bearerToken, userID string) ([]TwitterTweet, []TwitterMedia, error) {
	endpoint := "https://api.twitter.com/2/users/" + url.QueryEscape(userID) + "/tweets"

	q := url.Values{}
	q.Set("max_results", "100")
	q.Set("tweet.fields", "author_id,created_at,public_metrics,attachments")
	q.Set("expansions", "author_id,attachments.media_keys")
	q.Set("media.fields", "url,type,variants,public_metrics")
	q.Set("user.fields", "username")

	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint+"?"+q.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("User-Agent", "MemeVideoGen/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, nil, fmt.Errorf("rate limited")
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp TwitterAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, nil, err
	}

	var tweets []TwitterTweet
	var media []TwitterMedia

	if dataArr, ok := apiResp.Data.([]interface{}); ok {
		for _, item := range dataArr {
			if tweet, ok := item.(map[string]interface{}); ok {
				data, _ := json.Marshal(tweet)
				var t TwitterTweet
				json.Unmarshal(data, &t)
				tweets = append(tweets, t)
			}
		}
	}

	if apiResp.Includes != nil && apiResp.Includes.Media != nil {
		media = apiResp.Includes.Media
	}

	return tweets, media, nil
}

// downloadTwitterMedia downloads media from Twitter URL
func (sc *Scraper) downloadTwitterMedia(ctx context.Context, client *http.Client, mediaURL, username, outputDir string) (*model.SourceAsset, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", mediaURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	// Read content
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if len(body) == 0 {
		return nil, errors.New("empty response")
	}

	// Determine file extension from content type
	contentType := resp.Header.Get("Content-Type")
	ext := ".jpg"
	if strings.Contains(contentType, "png") {
		ext = ".png"
	} else if strings.Contains(contentType, "gif") {
		ext = ".gif"
	} else if strings.Contains(contentType, "webp") {
		ext = ".webp"
	}

	// Save file
	filename := fmt.Sprintf("twitter_%s_%d%s", username, time.Now().UnixNano(), ext)
	filepath := filepath.Join(outputDir, filename)

	if err := os.WriteFile(filepath, body, 0644); err != nil {
		return nil, err
	}

	// Calculate SHA256
	sha := sha256.Sum256(body)
	shaStr := fmt.Sprintf("%x", sha)

	asset := &model.SourceAsset{
		ID:        fmt.Sprintf("twitter_%s_%d", username, time.Now().UnixNano()),
		Kind:      model.SourceKindTwitter,
		SourceURL: mediaURL,
		MediaKey:  filename,
		MimeType:  contentType,
		AddedAt:   time.Now(),
		SHA256:    shaStr,
	}

	sc.logf("Twitter: Downloaded media from @%s: %s", username, filename)
	return asset, nil
}
