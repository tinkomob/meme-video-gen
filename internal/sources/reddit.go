package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"meme-video-gen/internal/model"
)

// redditPost represents a Reddit post from the API
type redditPost struct {
	Data redditPostData `json:"data"`
}

type redditPostData struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

type redditListing struct {
	Data redditListingData `json:"data"`
}

type redditListingData struct {
	Children []redditPost `json:"children"`
}

// ScrapeReddit extracts a single image from a Reddit subreddit.
// subredditURL can be in format: "r/subreddit", "r/subreddit/", "/r/subreddit", or "subreddit"
func (sc *Scraper) scrapeReddit(ctx context.Context, subredditURL string) (*model.SourceAsset, error) {
	// Normalize subreddit URL
	subredditName := subredditURL
	subredditName = strings.TrimSpace(subredditName)
	subredditName = strings.TrimPrefix(subredditName, "https://reddit.com/")
	subredditName = strings.TrimPrefix(subredditName, "https://www.reddit.com/")
	subredditName = strings.TrimPrefix(subredditName, "/r/")
	subredditName = strings.TrimPrefix(subredditName, "r/")
	subredditName = strings.TrimSuffix(subredditName, "/")

	if subredditName == "" {
		return nil, errors.New("invalid subreddit name")
	}

	// Construct Reddit API URL with JSON format
	apiURL := fmt.Sprintf("https://www.reddit.com/r/%s/new.json?limit=50", url.QueryEscape(subredditName))

	// Create HTTP request with proper User-Agent
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	// Execute request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch reddit API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("reddit API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse JSON response
	var listing redditListing
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return nil, fmt.Errorf("parse reddit response: %w", err)
	}

	// Find first image post
	for _, child := range listing.Data.Children {
		post := child.Data
		imgURL := extractImageURL(post.URL)
		if imgURL != "" {
			return sc.downloadAsset(ctx, imgURL, model.SourceKindReddit, fmt.Sprintf("r/%s", subredditName))
		}
	}

	return nil, errors.New("no reddit image found")
}

// extractImageURL extracts image URL from various Reddit post types
func extractImageURL(postURL string) string {
	if postURL == "" {
		return ""
	}

	// Direct image links from i.redd.it
	if strings.Contains(postURL, "i.redd.it") {
		if strings.HasSuffix(postURL, ".jpg") || strings.HasSuffix(postURL, ".png") || strings.HasSuffix(postURL, ".gif") {
			return postURL
		}
	}

	// imgur links
	if strings.Contains(postURL, "imgur.com") {
		// Convert imgur page URLs to direct image URLs
		if !strings.HasSuffix(postURL, ".jpg") && !strings.HasSuffix(postURL, ".png") && !strings.HasSuffix(postURL, ".gif") {
			// Extract imgur ID and construct direct URL
			re := regexp.MustCompile(`imgur\.com/([a-zA-Z0-9]+)`)
			if matches := re.FindStringSubmatch(postURL); len(matches) > 1 {
				return fmt.Sprintf("https://i.imgur.com/%s.jpg", matches[1])
			}
		}
		return postURL
	}

	return ""
}
