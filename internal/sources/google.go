package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"meme-video-gen/internal/model"
)

// GoogleKeywords represents the structure of google_keywords.json
type GoogleKeywords []string

// loadGoogleKeywords loads keywords from google_keywords.json
func loadGoogleKeywords(filename string) (GoogleKeywords, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return getDefaultGoogleKeywords(), nil
	}

	var keywords GoogleKeywords
	if err := json.Unmarshal(data, &keywords); err != nil {
		return getDefaultGoogleKeywords(), nil
	}

	if len(keywords) == 0 {
		return getDefaultGoogleKeywords(), nil
	}

	return keywords, nil
}

// getDefaultGoogleKeywords returns fallback keywords if file not found
func getDefaultGoogleKeywords() GoogleKeywords {
	return GoogleKeywords{
		"funny cat memes",
		"funny dog memes",
		"programming memes",
		"dank memes",
		"cursed images",
		"wholesome memes",
	}
}

// SerpAPIResponse represents the response from SerpAPI
type SerpAPIResponse struct {
	ImagesResults []struct {
		Position  int    `json:"position"`
		Thumbnail string `json:"thumbnail"`
		Original  string `json:"original"`
		Title     string `json:"title"`
		Source    string `json:"source"`
		Link      string `json:"link"`
	} `json:"images_results"`
	SearchMetadata struct {
		Status string `json:"status"`
	} `json:"search_metadata"`
}

// scrapeGoogleImages fetches one image from Google Images using SerpAPI
func (sc *Scraper) scrapeGoogleImages(ctx context.Context) (*model.SourceAsset, error) {
	if sc.cfg.SerpAPIKey == "" {
		return nil, fmt.Errorf("SERPAPI_KEY not configured")
	}

	sc.log.Infof("Google: starting Google Images scraping via SerpAPI")

	// Load keywords - try both possible paths
	keywordsPaths := []string{
		"internal/sources/google_keywords.json",
		"google_keywords.json",
		filepath.Join("internal", "sources", "google_keywords.json"),
	}

	var keywords GoogleKeywords
	var err error
	loaded := false

	for _, path := range keywordsPaths {
		keywords, err = loadGoogleKeywords(path)
		if err == nil && len(keywords) > 0 {
			loaded = true
			sc.log.Infof("Google: loaded keywords from %s", path)
			break
		}
	}

	if !loaded {
		sc.log.Errorf("Google: failed to load keywords from any path, using defaults")
		keywords = getDefaultGoogleKeywords()
	}

	sc.log.Infof("Google: loaded %d keywords", len(keywords))

	// Shuffle and try multiple keywords if needed
	rand.Shuffle(len(keywords), func(i, j int) {
		keywords[i], keywords[j] = keywords[j], keywords[i]
	})

	maxKeywordAttempts := 5
	if len(keywords) < maxKeywordAttempts {
		maxKeywordAttempts = len(keywords)
	}

	for keywordIdx := 0; keywordIdx < maxKeywordAttempts; keywordIdx++ {
		query := keywords[keywordIdx]
		sc.log.Infof("Google: trying keyword %d/%d: '%s'", keywordIdx+1, maxKeywordAttempts, query)

		// Build SerpAPI request
		apiURL := "https://serpapi.com/search.json"
		params := url.Values{}
		params.Set("engine", "google_images")
		params.Set("q", query)
		params.Set("api_key", sc.cfg.SerpAPIKey)
		params.Set("num", "10")
		params.Set("safe", "off")
		params.Set("ijn", "0")

		fullURL := fmt.Sprintf("%s?%s", apiURL, params.Encode())

		// Make HTTP request
		req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
		if err != nil {
			sc.log.Errorf("Google: failed to create request: %v", err)
			continue
		}

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			sc.log.Errorf("Google: request failed: %v", err)
			continue
		}

		if resp.StatusCode != 200 {
			resp.Body.Close()
			sc.log.Errorf("Google: SerpAPI returned status %d", resp.StatusCode)
			continue
		}

		// Parse response
		var serpResp SerpAPIResponse
		if err := json.NewDecoder(resp.Body).Decode(&serpResp); err != nil {
			resp.Body.Close()
			sc.log.Errorf("Google: failed to parse response: %v", err)
			continue
		}
		resp.Body.Close()

		if len(serpResp.ImagesResults) == 0 {
			sc.log.Errorf("Google: no images found for keyword '%s'", query)
			continue
		}

		sc.log.Infof("Google: found %d images for keyword '%s'", len(serpResp.ImagesResults), query)

		// Shuffle results and try to download
		rand.Shuffle(len(serpResp.ImagesResults), func(i, j int) {
			serpResp.ImagesResults[i], serpResp.ImagesResults[j] = serpResp.ImagesResults[j], serpResp.ImagesResults[i]
		})

		maxImageAttempts := 10
		if len(serpResp.ImagesResults) < maxImageAttempts {
			maxImageAttempts = len(serpResp.ImagesResults)
		}

		for imgIdx := 0; imgIdx < maxImageAttempts; imgIdx++ {
			img := serpResp.ImagesResults[imgIdx]
			imageURL := img.Original

			if imageURL == "" {
				continue
			}

			// Validate image URL
			if !strings.HasPrefix(imageURL, "http://") && !strings.HasPrefix(imageURL, "https://") {
				continue
			}

			// Check file extension
			lowerURL := strings.ToLower(imageURL)
			validExt := false
			for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp"} {
				if strings.Contains(lowerURL, ext) {
					validExt = true
					break
				}
			}
			if !validExt {
				continue
			}

			sc.log.Infof("Google: attempting to download image %d/%d from %s", imgIdx+1, maxImageAttempts, imageURL)

			// Download image
			asset, err := sc.downloadGoogleImage(ctx, imageURL, img.Source, query)
			if err != nil {
				sc.log.Errorf("Google: failed to download image: %v", err)
				continue
			}

			sc.log.Infof("Google: successfully downloaded image from Google Images")
			return asset, nil
		}

		sc.log.Errorf("Google: no valid images downloaded for keyword '%s'", query)
	}

	return nil, fmt.Errorf("failed to download any image after %d keyword attempts", maxKeywordAttempts)
}

// downloadGoogleImage downloads an image from the given URL and creates a SourceAsset
func (sc *Scraper) downloadGoogleImage(ctx context.Context, imageURL, source, query string) (*model.SourceAsset, error) {
	// Download image
	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.google.com/")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Read image data
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read image data: %w", err)
	}

	// Validate image size
	if len(imageData) < 1024 {
		return nil, fmt.Errorf("image too small: %d bytes", len(imageData))
	}

	if len(imageData) > 20*1024*1024 {
		return nil, fmt.Errorf("image too large: %d bytes", len(imageData))
	}

	// Calculate SHA256
	hash := sha256.Sum256(imageData)
	sha256Hash := hex.EncodeToString(hash[:])

	// Determine file extension
	ext := ".jpg"
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "png") {
		ext = ".png"
	} else if strings.Contains(contentType, "gif") {
		ext = ".gif"
	} else if strings.Contains(contentType, "webp") {
		ext = ".webp"
	}

	// Generate unique ID and filename
	id := fmt.Sprintf("google_%d", time.Now().UnixNano())
	mediaKey := sc.cfg.SourcesPrefix + id + ext

	// Upload to S3
	if err := sc.s3.PutBytes(ctx, mediaKey, imageData, contentType); err != nil {
		return nil, fmt.Errorf("upload to S3: %w", err)
	}

	// Create SourceAsset
	asset := &model.SourceAsset{
		ID:         id,
		Kind:       model.SourceKindUnknown,
		SourceURL:  imageURL,
		MediaKey:   mediaKey,
		MimeType:   contentType,
		AddedAt:    time.Now(),
		LastSeenAt: time.Now(),
		Used:       false,
		SHA256:     sha256Hash,
	}

	sc.log.Infof("Google: created asset with ID %s (SHA256: %s)", id, sha256Hash[:8])
	return asset, nil
}
