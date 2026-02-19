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

func looksLikeImage(data []byte) (kind string, ok bool) {
	if len(data) < 12 {
		return "", false
	}
	// PNG: 89 50 4E 47 0D 0A 1A 0A
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 &&
		data[4] == 0x0D && data[5] == 0x0A && data[6] == 0x1A && data[7] == 0x0A {
		return "png", true
	}
	// JPEG: FF D8 FF
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "jpeg", true
	}
	// WebP: RIFF....WEBP
	if data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F' &&
		data[8] == 'W' && data[9] == 'E' && data[10] == 'B' && data[11] == 'P' {
		return "webp", true
	}
	return "", false
}

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

// logfIfNotSilent logs to stdout only if Silent mode is disabled
func (sc *Scraper) logfIfNotSilent(format string, args ...interface{}) {
	if !sc.cfg.Silent {
		sc.logf(format, args...)
	}
}

// logIfNotSilent logs only if Silent mode is disabled
func (sc *Scraper) logIfNotSilent(format string, args ...interface{}) {
	if !sc.cfg.Silent && sc.log != nil {
		sc.log.Infof(format, args...)
	}
}

func (sc *Scraper) EnsureSources(ctx context.Context) error {
	sc.logIfNotSilent("sources: ensuring sources index (max=%d)", sc.cfg.MaxSources)

	// First, synchronize JSON with actual S3 files
	if err := sc.SyncWithS3(ctx); err != nil {
		sc.log.Errorf("sources: sync failed (continuing anyway): %v", err)
	}

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
		sc.logIfNotSilent("sources: already at max capacity (%d/%d)", len(sourcesIdx.Items), sc.cfg.MaxSources)
		sourcesIdx.UpdatedAt = time.Now()
		_ = sc.s3.WriteJSON(ctx, sc.cfg.SourcesJSONKey, &sourcesIdx)
		return nil
	}

	sc.logIfNotSilent("sources: loading source URLs")
	pinterestURLs, _ := sc.loadSourceURLs(ctx, "pinterest_urls.json")
	redditURLs, _ := sc.loadSourceURLs(ctx, "reddit_sources.json")
	twitterURLs, _ := sc.loadSourceURLs(ctx, "twitter_urls.json")

	sc.logIfNotSilent("sources: found %d pinterest, %d reddit, %d twitter URLs", len(pinterestURLs), len(redditURLs), len(twitterURLs))

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
		sc.logIfNotSilent("sources: no sources configured")
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

		sc.logIfNotSilent("sources: trying %s: %s", src.name, src.url)
		asset, err := src.scrape(ctx, src.url)
		if err != nil {
			sc.log.Errorf("sources: scrape %s %s failed: %v", src.name, src.url, err)
			continue
		}

		if asset != nil {
			if sc.assetExists(sourcesIdx, asset.SHA256) {
				sc.logIfNotSilent("sources: ⚠️  duplicate detected! Skipping %s asset (SHA256 already exists)", src.name)
				continue
			}

			// Also check for visual duplicates by image hash
			if asset.ImageHash != 0 && sc.assetExistsByImageHash(sourcesIdx, asset.ImageHash) {
				sc.logIfNotSilent("sources: ⚠️  visual duplicate detected! Skipping %s asset (ImageHash already exists)", src.name)
				continue
			}

			newAssets = append(newAssets, *asset)
			sourcesIdx.Items = append(sourcesIdx.Items, *asset)
			sourcesIdx.UpdatedAt = time.Now()

			// Update sources.json immediately after each successful upload
			if err := sc.s3.WriteJSON(ctx, sc.cfg.SourcesJSONKey, &sourcesIdx); err != nil {
				sc.log.Errorf("sources: failed to update sources.json after upload: %v", err)
			} else {
				sc.logIfNotSilent("sources: ✓ updated sources.json after adding %s asset (%d/%d, total=%d/%d)", src.name, len(newAssets), needed, len(sourcesIdx.Items), sc.cfg.MaxSources)
			}
		}
	}

	sc.logIfNotSilent("sources: index updated (added %d new assets, total=%d)", len(newAssets), len(sourcesIdx.Items))
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
		sc.logIfNotSilent("sources: deleted %d old assets", before-len(idx.Items))
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
	sc.logIfNotSilent("sources: trimmed %d excess assets", len(toDelete))
}

// SyncWithS3 synchronizes sources.json with actual files in S3 sources/ folder
func (sc *Scraper) SyncWithS3(ctx context.Context) error {
	sc.logIfNotSilent("sources: starting sync with S3 folder")

	// Read current index
	var sourcesIdx model.SourcesIndex
	found, err := sc.s3.ReadJSON(ctx, sc.cfg.SourcesJSONKey, &sourcesIdx)
	if err != nil {
		return fmt.Errorf("read sources.json: %w", err)
	}
	if !found {
		sourcesIdx = model.SourcesIndex{Items: []model.SourceAsset{}}
	}

	// List all files in sources/ folder
	objects, err := sc.s3.List(ctx, sc.cfg.SourcesPrefix)
	if err != nil {
		return fmt.Errorf("list S3 sources: %w", err)
	}

	// Create map of existing keys in JSON
	existingKeys := make(map[string]bool)
	for _, item := range sourcesIdx.Items {
		existingKeys[item.MediaKey] = true
	}

	// Create map of actual keys in S3
	actualKeys := make(map[string]bool)
	for _, obj := range objects {
		actualKeys[obj.Key] = true
	}

	// Remove entries from JSON that don't exist in S3
	originalCount := len(sourcesIdx.Items)
	filtered := make([]model.SourceAsset, 0)
	for _, item := range sourcesIdx.Items {
		if actualKeys[item.MediaKey] {
			filtered = append(filtered, item)
		} else {
			sc.logIfNotSilent("sources: removing orphaned entry from JSON: %s", item.MediaKey)
		}
	}
	sourcesIdx.Items = filtered

	removedCount := originalCount - len(filtered)
	if removedCount > 0 {
		sc.logIfNotSilent("sources: removed %d orphaned entries from JSON", removedCount)
	}

	// Delete orphaned files in S3 that are not tracked in JSON
	orphanedFiles := 0
	deletedFiles := 0
	for key := range actualKeys {
		if !existingKeys[key] {
			orphanedFiles++
			sc.logIfNotSilent("sources: deleting orphaned file from S3: %s", key)
			if err := sc.s3.Delete(ctx, key); err != nil {
				sc.log.Errorf("sources: failed to delete orphaned file %s: %v", key, err)
			} else {
				deletedFiles++
			}
		}
	}
	if orphanedFiles > 0 {
		sc.logIfNotSilent("sources: deleted %d/%d orphaned files from S3", deletedFiles, orphanedFiles)
	}

	// Update JSON
	sourcesIdx.UpdatedAt = time.Now()
	if err := sc.s3.WriteJSON(ctx, sc.cfg.SourcesJSONKey, &sourcesIdx); err != nil {
		return fmt.Errorf("write sources.json: %w", err)
	}

	sc.logIfNotSilent("sources: sync complete - JSON entries: %d, S3 files: %d, removed: %d, orphaned: %d",
		len(sourcesIdx.Items), len(objects), removedCount, orphanedFiles)
	return nil
}

func (sc *Scraper) assetExists(idx model.SourcesIndex, sha256 string) bool {
	return lo.ContainsBy(idx.Items, func(a model.SourceAsset) bool { return a.SHA256 == sha256 })
}

func (sc *Scraper) assetExistsByImageHash(idx model.SourcesIndex, imageHash uint64) bool {
	return lo.ContainsBy(idx.Items, func(a model.SourceAsset) bool { return a.ImageHash == imageHash })
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
		sc.logIfNotSilent("sources: all sources used, resetting for rotation")
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

	// Don't mark as used here - will be marked after successful download in generateOne
	return &selected, nil
}

func (sc *Scraper) RemoveSourceFromIndex(ctx context.Context, id string) error {
	var idx model.SourcesIndex
	found, err := sc.s3.ReadJSON(ctx, sc.cfg.SourcesJSONKey, &idx)
	if err != nil || !found {
		return fmt.Errorf("no sources.json")
	}

	newItems := make([]model.SourceAsset, 0, len(idx.Items))
	for _, item := range idx.Items {
		if item.ID != id {
			newItems = append(newItems, item)
		}
	}

	if len(newItems) == len(idx.Items) {
		return fmt.Errorf("source %s not found", id)
	}

	idx.Items = newItems
	idx.UpdatedAt = time.Now()

	if err := sc.s3.WriteJSON(ctx, sc.cfg.SourcesJSONKey, &idx); err != nil {
		return fmt.Errorf("failed to update sources.json: %w", err)
	}

	sc.logIfNotSilent("sources: removed source %s from index", id)
	return nil
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

			sc.logIfNotSilent("sources: marked source %s as used and updated sources.json", id)
			return nil
		}
	}

	return fmt.Errorf("source %s not found", id)
}

func (sc *Scraper) SourceExistsInS3(ctx context.Context, asset *model.SourceAsset) bool {
	if asset == nil || asset.MediaKey == "" {
		return false
	}
	_, _, err := sc.s3.GetBytes(ctx, asset.MediaKey)
	return err == nil
}

func (sc *Scraper) DownloadSourceToTemp(ctx context.Context, asset *model.SourceAsset) (string, error) {
	if asset == nil {
		return "", fmt.Errorf("asset is nil")
	}
	if asset.MediaKey == "" {
		return "", fmt.Errorf("asset.MediaKey is empty (asset ID: %s)", asset.ID)
	}

	data, ct, err := sc.s3.GetBytes(ctx, asset.MediaKey)
	if err != nil {
		return "", fmt.Errorf("s3.GetBytes failed for key '%s': %w", asset.MediaKey, err)
	}
	if kind, ok := looksLikeImage(data); !ok {
		head := data
		if len(head) > 32 {
			head = head[:32]
		}
		return "", fmt.Errorf("downloaded source is not a valid image (key=%s id=%s ct=%q size=%d head=% x)", asset.MediaKey, asset.ID, ct, len(data), head)
	} else {
		_ = kind
	}
	if len(data) < 1024 {
		return "", fmt.Errorf("downloaded source too small (key=%s id=%s ct=%q size=%d)", asset.MediaKey, asset.ID, ct, len(data))
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
		sc.logIfNotSilent("sources: loaded %s from S3: %s", filename, key)
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
	sc.logIfNotSilent("sources: S3 load failed for %s (%v), trying local paths", filename, err)
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
