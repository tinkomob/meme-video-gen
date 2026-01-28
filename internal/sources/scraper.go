package sources

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samber/lo"
	"github.com/tidwall/gjson"

	"meme-video-gen/internal"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/s3"
)

type Scraper struct {
	cfg internal.Config
	s3  s3.Client
	log *logging.Logger
}

func NewScraper(cfg internal.Config, s3c s3.Client, log *logging.Logger) *Scraper {
	return &Scraper{cfg: cfg, s3: s3c, log: log}
}

// logf logs a message to stdout for CLI usage
func (sc *Scraper) logf(format string, args ...interface{}) {
	fmt.Println(fmt.Sprintf(format, args...))
	if sc.log != nil {
		sc.log.Infof(format, args...)
	}
}

func (sc *Scraper) EnsureSources(ctx context.Context) error {
	sc.log.Infof("sources: ensuring sources index (max=%d)", sc.cfg.MaxSources)
	var sourcesIdx model.SourcesIndex
	found, err := sc.s3.ReadJSON(ctx, sc.cfg.SourcesJSONKey, &sourcesIdx)
	if err != nil {
		return fmt.Errorf("read sources.json: %w", err)
	}
	if !found {
		sourcesIdx = model.SourcesIndex{Items: []model.SourceAsset{}}
	}

	sc.cleanupOld(ctx, &sourcesIdx)
	sc.trimExcess(ctx, &sourcesIdx)

	if len(sourcesIdx.Items) >= sc.cfg.MaxSources {
		sc.log.Infof("sources: already at max capacity (%d/%d)", len(sourcesIdx.Items), sc.cfg.MaxSources)
		sourcesIdx.UpdatedAt = time.Now()
		_ = sc.s3.WriteJSON(ctx, sc.cfg.SourcesJSONKey, &sourcesIdx)
		return nil
	}

	sc.log.Infof("sources: loading source URLs")
	pinterestURLs, _ := sc.loadSourceURLs(ctx, "pinterest_urls.json")
	redditURLs, _ := sc.loadSourceURLs(ctx, "reddit_sources.json")
	twitterURLs, _ := sc.loadSourceURLs(ctx, "twitter_urls.json")

	sc.log.Infof("sources: found %d pinterest, %d reddit, %d twitter URLs", len(pinterestURLs), len(redditURLs), len(twitterURLs))

	needed := sc.cfg.MaxSources - len(sourcesIdx.Items)
	if needed <= 0 {
		return nil
	}

	var newAssets []model.SourceAsset

	// Create a list of all available sources with their scrapers
	type sourceFunc struct {
		name   string
		url    string
		scrape func(ctx context.Context, url string) (*model.SourceAsset, error)
	}

	var allSources []sourceFunc

	// Add Google Images if configured
	if sc.cfg.SerpAPIKey != "" {
		allSources = append(allSources, sourceFunc{
			name: "google",
			url:  "google_images",
			scrape: func(ctx context.Context, _ string) (*model.SourceAsset, error) {
				return sc.scrapeGoogleImages(ctx)
			},
		})
	}

	// Add Pinterest sources
	for _, u := range pinterestURLs {
		allSources = append(allSources, sourceFunc{
			name: "pinterest",
			url:  u,
			scrape: func(ctx context.Context, url string) (*model.SourceAsset, error) {
				return sc.ScrapePinterest(ctx, url)
			},
		})
	}

	// Add Reddit sources
	for _, u := range redditURLs {
		allSources = append(allSources, sourceFunc{
			name: "reddit",
			url:  u,
			scrape: func(ctx context.Context, url string) (*model.SourceAsset, error) {
				return sc.scrapeReddit(ctx, url)
			},
		})
	}

	// Add Twitter sources
	for _, u := range twitterURLs {
		allSources = append(allSources, sourceFunc{
			name: "twitter",
			url:  u,
			scrape: func(ctx context.Context, url string) (*model.SourceAsset, error) {
				return sc.scrapeTwitter(ctx, url)
			},
		})
	}

	if len(allSources) == 0 {
		sc.log.Infof("sources: no sources configured")
		sourcesIdx.UpdatedAt = time.Now()
		_ = sc.s3.WriteJSON(ctx, sc.cfg.SourcesJSONKey, &sourcesIdx)
		return nil
	}

	// Shuffle sources for random selection
	rand.Shuffle(len(allSources), func(i, j int) {
		allSources[i], allSources[j] = allSources[j], allSources[i]
	})

	// Try sources in random order until we have enough or run out of sources
	for _, src := range allSources {
		if len(newAssets) >= needed {
			break
		}

		sc.log.Infof("sources: trying %s: %s", src.name, src.url)
		asset, err := src.scrape(ctx, src.url)
		if err != nil {
			sc.log.Errorf("sources: scrape %s %s failed: %v", src.name, src.url, err)
			continue
		}

		if asset != nil {
			if sc.assetExists(sourcesIdx, asset.SHA256) {
				sc.log.Infof("sources: ⚠️  duplicate detected! Skipping %s asset (SHA256 already exists)", src.name)
				continue
			}
			
			newAssets = append(newAssets, *asset)
			sourcesIdx.Items = append(sourcesIdx.Items, *asset)
			sourcesIdx.UpdatedAt = time.Now()

			// Update sources.json immediately after each successful upload
			if err := sc.s3.WriteJSON(ctx, sc.cfg.SourcesJSONKey, &sourcesIdx); err != nil {
				sc.log.Errorf("sources: failed to update sources.json after upload: %v", err)
			} else {
				sc.log.Infof("sources: ✓ updated sources.json after adding %s asset (%d/%d, total=%d/%d)", src.name, len(newAssets), needed, len(sourcesIdx.Items), sc.cfg.MaxSources)
			}
		}
	}

	sc.log.Infof("sources: index updated (added %d new assets, total=%d)", len(newAssets), len(sourcesIdx.Items))
	return nil
}

func (sc *Scraper) cleanupOld(ctx context.Context, idx *model.SourcesIndex) {
	cutoff := time.Now().Add(-sc.cfg.MaxAge)
	before := len(idx.Items)
	idx.Items = lo.Filter(idx.Items, func(a model.SourceAsset, _ int) bool {
		if a.AddedAt.Before(cutoff) {
			_ = sc.s3.Delete(ctx, a.MediaKey)
			return false
		}
		return true
	})
	if len(idx.Items) < before {
		sc.log.Infof("sources: deleted %d old assets", before-len(idx.Items))
	}
}

func (sc *Scraper) trimExcess(ctx context.Context, idx *model.SourcesIndex) {
	if len(idx.Items) <= sc.cfg.MaxSources {
		return
	}
	sorted := sortByAddedAt(idx.Items, false)
	toDelete := sorted[sc.cfg.MaxSources:]
	for _, a := range toDelete {
		_ = sc.s3.Delete(ctx, a.MediaKey)
	}
	idx.Items = sorted[:sc.cfg.MaxSources]
	sc.log.Infof("sources: trimmed %d excess assets", len(toDelete))
}

func (sc *Scraper) assetExists(idx model.SourcesIndex, sha256 string) bool {
	return lo.ContainsBy(idx.Items, func(a model.SourceAsset) bool { return a.SHA256 == sha256 })
}

func (sc *Scraper) GetRandomUnusedSource(ctx context.Context) (*model.SourceAsset, error) {
	var idx model.SourcesIndex
	found, err := sc.s3.ReadJSON(ctx, sc.cfg.SourcesJSONKey, &idx)
	if err != nil || !found {
		return nil, fmt.Errorf("no sources")
	}

	unused := lo.Filter(idx.Items, func(a model.SourceAsset, _ int) bool { return !a.Used })
	if len(unused) == 0 {
		// If all sources are used, reset them and start from beginning
		sc.log.Infof("sources: all sources used, resetting for rotation")
		for i := range idx.Items {
			idx.Items[i].Used = false
		}
		idx.UpdatedAt = time.Now()
		if err := sc.s3.WriteJSON(ctx, sc.cfg.SourcesJSONKey, &idx); err != nil {
			sc.log.Errorf("sources: failed to reset used flag: %v", err)
		}
		unused = idx.Items
	}

	if len(unused) == 0 {
		return nil, fmt.Errorf("no sources available")
	}

	i := randomIndex(len(unused))
	selected := unused[i]

	// Immediately mark this source as used and update sources.json
	if err := sc.MarkSourceUsed(ctx, selected.ID); err != nil {
		sc.log.Errorf("sources: failed to mark source as used: %v", err)
	}

	return &selected, nil
}

func (sc *Scraper) MarkSourceUsed(ctx context.Context, id string) error {
	var idx model.SourcesIndex
	found, err := sc.s3.ReadJSON(ctx, sc.cfg.SourcesJSONKey, &idx)
	if err != nil || !found {
		return fmt.Errorf("no sources.json")
	}

	for i := range idx.Items {
		if idx.Items[i].ID == id {
			idx.Items[i].Used = true
			idx.Items[i].LastSeenAt = time.Now()
			idx.UpdatedAt = time.Now()

			// Update sources.json immediately
			if err := sc.s3.WriteJSON(ctx, sc.cfg.SourcesJSONKey, &idx); err != nil {
				return fmt.Errorf("failed to update sources.json: %w", err)
			}

			sc.log.Infof("sources: marked source %s as used and updated sources.json", id)
			return nil
		}
	}

	return fmt.Errorf("source %s not found", id)
}

func (sc *Scraper) DownloadSourceToTemp(ctx context.Context, asset *model.SourceAsset) (string, error) {
	if asset == nil {
		return "", fmt.Errorf("asset is nil")
	}
	if asset.MediaKey == "" {
		return "", fmt.Errorf("asset.MediaKey is empty (asset ID: %s)", asset.ID)
	}

	data, _, err := sc.s3.GetBytes(ctx, asset.MediaKey)
	if err != nil {
		return "", fmt.Errorf("s3.GetBytes failed for key '%s': %w", asset.MediaKey, err)
	}
	ext := filepath.Ext(asset.MediaKey)
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("source-%s%s", asset.ID, ext))
	return tmpFile, os.WriteFile(tmpFile, data, 0o644)
}

func (sc *Scraper) loadSourceURLs(ctx context.Context, filename string) ([]string, error) {
	// Try to load from S3 first
	key := sc.cfg.PayloadPrefix + filename
	data, _, err := sc.s3.GetBytes(ctx, key)
	if err == nil && data != nil {
		sc.log.Infof("sources: loaded %s from S3: %s", filename, key)
		res := gjson.GetBytes(data, "@this")
		if !res.IsArray() {
			return nil, fmt.Errorf("%s must be array", filename)
		}
		var out []string
		for _, item := range res.Array() {
			s := strings.TrimSpace(item.String())
			if s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	}

	// Fallback to local files
	sc.log.Infof("sources: S3 load failed for %s (%v), trying local paths", filename, err)
	paths := []string{
		filename,
		"cmd/" + filename,
		"internal/sources/" + filename,
		"./internal/sources/" + filename,
	}

	var localData []byte
	var lastErr error

	for _, path := range paths {
		if d, readErr := os.ReadFile(path); readErr == nil {
			localData = d
			break
		} else {
			lastErr = readErr
		}
	}

	if localData == nil {
		return nil, fmt.Errorf("%s not found in any path: %v", filename, lastErr)
	}

	res := gjson.GetBytes(localData, "@this")
	if !res.IsArray() {
		return nil, fmt.Errorf("%s must be array", filename)
	}
	var out []string
	for _, item := range res.Array() {
		s := strings.TrimSpace(item.String())
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}
