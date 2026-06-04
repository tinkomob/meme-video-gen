package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"meme-video-gen/internal/model"
)

const (
	humorAPILimit   = 10
	apiLeagueLimit  = 50
)

func (sc *Scraper) scrapeMemeAPI(ctx context.Context) (*model.SourceAsset, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://meme-api.com/gimme", nil)
	if err != nil {
		return nil, err
	}
	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("meme-api: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result struct {
		URL  string `json:"url"`
		NSFW bool   `json:"nsfw"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("meme-api: parse response: %w", err)
	}
	if result.URL == "" {
		return nil, fmt.Errorf("meme-api: empty url in response")
	}
	if result.NSFW {
		return nil, fmt.Errorf("meme-api: nsfw content, skipping")
	}
	return sc.downloadAsset(ctx, result.URL, model.SourceKindMemeAPI, result.URL)
}

func (sc *Scraper) scrapeHumorAPI(ctx context.Context) (*model.SourceAsset, error) {
	if sc.cfg.HumorAPIKey == "" {
		return nil, fmt.Errorf("humorapi: HUMOR_API_KEY not set")
	}
	if !sc.checkAndIncrementHumorAPI(ctx, humorAPILimit) {
		return nil, fmt.Errorf("humorapi: daily limit of %d reached", humorAPILimit)
	}

	url := "https://humorapi.com/memes/random?api-key=" + sc.cfg.HumorAPIKey
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("humorapi: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("humorapi: parse response: %w", err)
	}
	if result.URL == "" {
		return nil, fmt.Errorf("humorapi: empty url in response")
	}
	return sc.downloadAsset(ctx, result.URL, model.SourceKindHumorAPI, result.URL)
}

func (sc *Scraper) scrapeAPILeague(ctx context.Context) (*model.SourceAsset, error) {
	if sc.cfg.APILeagueKey == "" {
		return nil, fmt.Errorf("apileague: APILEAGUE_API_KEY not set")
	}
	if !sc.checkAndIncrementAPILeague(ctx, apiLeagueLimit) {
		return nil, fmt.Errorf("apileague: daily limit of %d reached", apiLeagueLimit)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.apileague.com/retrieve-random-meme?max-age-days=30", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", sc.cfg.APILeagueKey)
	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("apileague: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("apileague: parse response: %w", err)
	}
	if result.URL == "" {
		return nil, fmt.Errorf("apileague: empty url in response")
	}
	return sc.downloadAsset(ctx, result.URL, model.SourceKindAPILeague, result.URL)
}
