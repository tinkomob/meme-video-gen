package video

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/mowshon/moviego"
	"github.com/samber/lo"

	"meme-video-gen/internal"
	"meme-video-gen/internal/audio"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/s3"
	"meme-video-gen/internal/sources"
)

type Generator struct {
	cfg          internal.Config
	s3           s3.Client
	log          *logging.Logger
	audioIdx     *audio.Indexer
	sourcesScr   *sources.Scraper
	memesJSONMux sync.Mutex // Protects concurrent access to memes.json
}

func NewGenerator(cfg internal.Config, s3c s3.Client, log *logging.Logger, audioIdx *audio.Indexer, sourcesScr *sources.Scraper) *Generator {
	return &Generator{cfg: cfg, s3: s3c, log: log, audioIdx: audioIdx, sourcesScr: sourcesScr}
}

func (g *Generator) EnsureMemes(ctx context.Context) error {
	g.log.Infof("video: ensuring memes index")

	// First, synchronize JSON with actual S3 files
	if err := g.SyncWithS3(ctx); err != nil {
		g.log.Errorf("memes: sync failed (continuing anyway): %v", err)
	}

	// Check if we need to generate more memes (WITHOUT holding the lock)
	g.memesJSONMux.Lock()
	var memesIdx model.MemesIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &memesIdx)
	if err != nil {
		g.memesJSONMux.Unlock()
		return fmt.Errorf("read memes.json: %w", err)
	}
	if !found {
		memesIdx = model.MemesIndex{Items: []model.Meme{}}
	}

	currentCount := len(memesIdx.Items)
	if currentCount >= g.cfg.MaxMemes {
		g.log.Infof("video: already at max memes (%d)", currentCount)
		memesIdx.UpdatedAt = time.Now()
		_ = g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &memesIdx)
		g.memesJSONMux.Unlock()
		return nil
	}

	needed := g.cfg.MaxMemes - currentCount
	g.memesJSONMux.Unlock() // RELEASE LOCK before long-running generation

	g.log.Infof("video: need to generate %d more memes", needed)

	// Generate memes WITHOUT holding the lock
	generatedMemes := make([]*model.Meme, 0, needed)
	for i := 0; i < needed; i++ {
		g.log.Infof("video: generating meme %d/%d", i+1, needed)
		meme, err := g.generateOne(ctx, &memesIdx)
		if err != nil {
			g.log.Errorf("video: failed to generate meme %d/%d: %v", i+1, needed, err)
			continue
		}
		generatedMemes = append(generatedMemes, meme)
	}

	// NOW acquire lock and update index with all generated memes at once
	g.memesJSONMux.Lock()

	// Re-read to get latest state (another goroutine might have modified it)
	found, err = g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &memesIdx)
	if err != nil {
		g.log.Errorf("video: failed to re-read memes.json: %w", err)
		memesIdx = model.MemesIndex{Items: []model.Meme{}}
	}
	if !found {
		memesIdx = model.MemesIndex{Items: []model.Meme{}}
	}

	// Add all generated memes
	for _, meme := range generatedMemes {
		g.log.Infof("video: adding generated meme %s to index", meme.ID)
		memesIdx.Items = append(memesIdx.Items, *meme)
	}

	// Trim if we exceeded max (need to be careful with lock management)
	var toDelete []*model.Meme
	if len(memesIdx.Items) > g.cfg.MaxMemes {
		sorted := sortMemesByCreated(memesIdx.Items, false)
		for i := g.cfg.MaxMemes; i < len(sorted); i++ {
			toDelete = append(toDelete, &sorted[i])
		}
		memesIdx.Items = sorted[:g.cfg.MaxMemes]
		g.log.Infof("video: identified %d old memes to delete", len(toDelete))
	}

	// Update JSON first (while still holding lock)
	memesIdx.UpdatedAt = time.Now()
	g.log.Infof("video: saving memes.json with %d items", len(memesIdx.Items))
	if err := g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &memesIdx); err != nil {
		g.log.Errorf("video: CRITICAL ERROR - failed to save memes.json with %d items: %v", len(memesIdx.Items), err)
		g.memesJSONMux.Unlock()
		return fmt.Errorf("write memes.json: %w", err)
	}
	g.log.Infof("video: memes.json updated successfully with %d items", len(memesIdx.Items))

	// Unlock to allow other operations
	g.memesJSONMux.Unlock()

	// Delete old memes from S3 (outside the lock)
	if len(toDelete) > 0 {
		g.log.Infof("video: deleting %d old memes from S3", len(toDelete))
		for _, m := range toDelete {
			g.log.Infof("video: deleting old meme %s from S3", m.ID)
			_ = g.s3.Delete(ctx, m.VideoKey)
			_ = g.s3.Delete(ctx, m.ThumbKey)
		}
	}

	g.log.Infof("video: memes updated successfully (total=%d, deleted=%d)", len(memesIdx.Items), len(toDelete))
	return nil
}

// GenerateOneMeme generates a single meme and returns it
func (g *Generator) GenerateOneMeme(ctx context.Context) (*model.Meme, error) {
	g.log.Infof("GenerateOneMeme: START")

	// Generate meme FIRST (without lock) - this takes time (downloading, processing)
	memesIdx := model.MemesIndex{Items: []model.Meme{}}
	meme, err := g.generateOne(ctx, &memesIdx)
	if err != nil {
		g.log.Errorf("GenerateOneMeme: failed to generate: %v", err)
		return nil, err
	}
	g.log.Infof("GenerateOneMeme: generated meme %s, now updating index", meme.ID)

	// ONLY NOW acquire lock to update the index (quick operation)
	g.memesJSONMux.Lock()
	defer g.memesJSONMux.Unlock()

	// Re-read to get latest state
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &memesIdx)
	if err != nil {
		g.log.Errorf("GenerateOneMeme: failed to read memes.json: %v", err)
		memesIdx = model.MemesIndex{Items: []model.Meme{}}
	}
	if !found {
		memesIdx = model.MemesIndex{Items: []model.Meme{}}
	}

	// Add meme to index
	memesIdx.Items = append(memesIdx.Items, *meme)
	memesIdx.UpdatedAt = time.Now()

	maxRetries := 3
	var lastErr error
	for retry := 0; retry < maxRetries; retry++ {
		if err := g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &memesIdx); err != nil {
			lastErr = err
			g.log.Warnf("GenerateOneMeme: attempt %d/%d to update memes.json failed: %v, retrying...", retry+1, maxRetries, err)
			time.Sleep(time.Duration(retry+1) * 500 * time.Millisecond)
			continue
		}
		g.log.Infof("GenerateOneMeme: ✓ SUCCESS - meme added to index: %s", meme.ID)
		return meme, nil
	}

	// If we get here, all retries failed
	g.log.Errorf("GenerateOneMeme: CRITICAL - failed to update memes.json after %d attempts (last error: %v). Meme files exist in S3 but are not indexed: %s, %s",
		maxRetries, lastErr, meme.VideoKey, meme.ThumbKey)
	return meme, fmt.Errorf("critical: failed to update memes.json: %w", lastErr)
}

func (g *Generator) generateOne(ctx context.Context, memesIdx *model.MemesIndex) (*model.Meme, error) {
	g.log.Infof("video: generateOne started")

	song, err := g.audioIdx.GetRandomSong(ctx)
	if err != nil {
		return nil, fmt.Errorf("get song: %w", err)
	}
	g.log.Infof("video: got song %s", song.ID)

	// Try up to 5 times to find a valid source
	var source *model.SourceAsset
	for attempt := 0; attempt < 5; attempt++ {
		src, err := g.sourcesScr.GetRandomUnusedSource(ctx)
		if err != nil {
			return nil, fmt.Errorf("get source: %w", err)
		}

		// Check if file exists in S3 before trying to download
		if !g.sourcesScr.SourceExistsInS3(ctx, src) {
			g.log.Warnf("source %s file not found in S3 (%s), removing from index", src.ID, src.MediaKey)
			if err := g.sourcesScr.RemoveSourceFromIndex(ctx, src.ID); err != nil {
				g.log.Errorf("failed to remove source from index: %v", err)
			}
			continue
		}

		source = src
		break
	}

	if source == nil {
		return nil, fmt.Errorf("failed to find valid source after 5 attempts")
	}

	audioPath, err := g.audioIdx.DownloadSongToTemp(ctx, song)
	if err != nil {
		return nil, fmt.Errorf("download song: %w", err)
	}
	defer os.Remove(audioPath)

	sourcePath, err := g.sourcesScr.DownloadSourceToTemp(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("download source: %w", err)
	}
	defer os.Remove(sourcePath)

	// Validate source file exists and has reasonable size
	srcInfo, err := os.Stat(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("source file stat: %w", err)
	}
	if srcInfo.Size() < 1024 { // Less than 1KB is probably invalid
		g.log.Infof("source file too small: %d bytes, skipping", srcInfo.Size())
		return nil, fmt.Errorf("source file too small: %d bytes", srcInfo.Size())
	}

	videoPath := filepath.Join(os.TempDir(), fmt.Sprintf("meme-%d.mp4", time.Now().UnixNano()))
	defer os.Remove(videoPath)

	// Create video from image + audio using ffmpeg directly
	if err := createVideoFromImageAndAudio(sourcePath, audioPath, videoPath, g.log); err != nil {
		g.log.Infof("failed to create video from %s + audio: %v", sourcePath, err)
		return nil, fmt.Errorf("create video: %w", err)
	}

	videoData, err := os.ReadFile(videoPath)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(videoData)
	hash := hex.EncodeToString(h[:])

	if g.memeExists(*memesIdx, hash) {
		return nil, fmt.Errorf("duplicate sha256")
	}

	memeID := fmt.Sprintf("meme-%d", time.Now().UnixNano())
	videoKey := g.cfg.MemesPrefix + memeID + ".mp4"
	thumbKey := g.cfg.MemesPrefix + memeID + "_thumb.jpg"

	// Upload video to S3 - CRITICAL step
	if err := g.s3.PutBytes(ctx, videoKey, videoData, "video/mp4"); err != nil {
		g.log.Errorf("CRITICAL: failed to upload video to S3: %v", err)
		return nil, fmt.Errorf("upload video: %w", err)
	}

	g.log.Infof("video uploaded to S3: %s (%d bytes)", videoKey, len(videoData))

	// Extract and upload thumbnail (non-critical, continue even if fails)
	thumbPath := filepath.Join(os.TempDir(), fmt.Sprintf("thumb-%d.jpg", time.Now().UnixNano()))
	defer os.Remove(thumbPath)
	if err := extractThumbnail(videoPath, thumbPath, g.log); err != nil {
		g.log.Warnf("failed to extract thumbnail: %v, using placeholder", err)
		_ = g.s3.PutBytes(ctx, thumbKey, []byte{}, "image/jpeg")
	} else {
		thumbData, err := os.ReadFile(thumbPath)
		if err != nil {
			g.log.Warnf("failed to read thumbnail file: %v", err)
			_ = g.s3.PutBytes(ctx, thumbKey, []byte{}, "image/jpeg")
		} else {
			if err := g.s3.PutBytes(ctx, thumbKey, thumbData, "image/jpeg"); err != nil {
				g.log.Warnf("failed to upload thumbnail: %v", err)
			}
		}
	}

	// Mark source as used (non-critical)
	if err := g.sourcesScr.MarkSourceUsed(ctx, source.ID); err != nil {
		g.log.Warnf("failed to mark source as used: %v", err)
	}

	// Delete source file from S3 (non-critical)
	if err := g.s3.Delete(ctx, source.MediaKey); err != nil {
		g.log.Warnf("failed to delete source from S3: %v", err)
	}

	title := fmt.Sprintf("%s — %s", song.Author, song.Title)

	meme := &model.Meme{
		ID:        memeID,
		Title:     title,
		VideoKey:  videoKey,
		ThumbKey:  thumbKey,
		SongID:    song.ID,
		SourceID:  source.ID,
		CreatedAt: time.Now(),
		SHA256:    hash,
	}

	g.log.Infof("video: meme created successfully: ID=%s, VideoKey=%s, ThumbKey=%s",
		meme.ID, meme.VideoKey, meme.ThumbKey)

	return meme, nil
}

func (g *Generator) memeExists(idx model.MemesIndex, sha256 string) bool {
	return lo.ContainsBy(idx.Items, func(m model.Meme) bool { return m.SHA256 == sha256 })
}

func (g *Generator) GetRandomMeme(ctx context.Context) (*model.Meme, error) {
	var idx model.MemesIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &idx)
	if err != nil {
		g.log.Errorf("failed to read memes.json from S3 (key=%s): %v", g.cfg.MemesJSONKey, err)
		return nil, fmt.Errorf("read memes.json: %w", err)
	}
	if !found {
		g.log.Infof("memes.json not found in S3 (key=%s), no memes available", g.cfg.MemesJSONKey)
		return nil, fmt.Errorf("memes.json not found")
	}
	if len(idx.Items) == 0 {
		g.log.Infof("memes.json exists but empty, need to generate memes")
		return nil, fmt.Errorf("no memes in index")
	}
	i := randomIndex(len(idx.Items))
	g.log.Infof("returning random meme (id=%s, total=%d)", idx.Items[i].ID, len(idx.Items))
	return &idx.Items[i], nil
}

func (g *Generator) DownloadMemeToTemp(ctx context.Context, meme *model.Meme) (string, error) {
	data, _, err := g.s3.GetBytes(ctx, meme.VideoKey)
	if err != nil {
		return "", err
	}
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("meme-%s.mp4", meme.ID))
	return tmpFile, os.WriteFile(tmpFile, data, 0o644)
}

// DeleteMeme deletes a meme from S3 and updates memes.json
func (g *Generator) DeleteMeme(ctx context.Context, memeID string) error {
	g.log.Infof("deleteMemeb: START - attempting to delete memeID=%s", memeID)

	// Lock to prevent concurrent modifications
	g.memesJSONMux.Lock()
	g.log.Infof("deleteMemeb: acquired lock")
	defer g.memesJSONMux.Unlock()

	// Read memes.json
	var idx model.MemesIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &idx)
	if err != nil {
		g.log.Errorf("deleteMemeb: failed to read memes.json - err=%v", err)
		return fmt.Errorf("failed to read memes.json: %w", err)
	}
	if !found {
		g.log.Warnf("deleteMemeb: memes.json not found in S3")
		return fmt.Errorf("memes.json not found")
	}

	g.log.Infof("deleteMemeb: loaded memes.json with %d items", len(idx.Items))

	// Find and remove the meme
	memeIndex := -1
	var memeToDelete *model.Meme
	for i, m := range idx.Items {
		g.log.Infof("deleteMemeb: comparing id=%s with target=%s", m.ID, memeID)
		if m.ID == memeID {
			memeIndex = i
			memeToDelete = &m
			g.log.Infof("deleteMemeb: found meme at index %d", i)
			break
		}
	}

	if memeIndex == -1 {
		g.log.Warnf("deleteMemeb: meme not found in JSON: %s (already deleted or doesn't exist)", memeID)
		return nil // Not an error - meme might have been already deleted
	}

	g.log.Infof("deleteMemeb: deleting meme - ID=%s, VideoKey=%s, ThumbKey=%s",
		memeToDelete.ID, memeToDelete.VideoKey, memeToDelete.ThumbKey)

	// Delete video and thumbnail from S3
	if memeToDelete.VideoKey != "" {
		g.log.Infof("deleteMemeb: deleting video from S3 - key=%s", memeToDelete.VideoKey)
		if err := g.s3.Delete(ctx, memeToDelete.VideoKey); err != nil {
			g.log.Errorf("deleteMemeb: failed to delete video from S3 - key=%s, err=%v", memeToDelete.VideoKey, err)
		} else {
			g.log.Infof("deleteMemeb: successfully deleted video from S3 - key=%s", memeToDelete.VideoKey)
		}
	} else {
		g.log.Warnf("deleteMemeb: VideoKey is empty for meme %s", memeID)
	}

	if memeToDelete.ThumbKey != "" {
		g.log.Infof("deleteMemeb: deleting thumbnail from S3 - key=%s", memeToDelete.ThumbKey)
		if err := g.s3.Delete(ctx, memeToDelete.ThumbKey); err != nil {
			g.log.Errorf("deleteMemeb: failed to delete thumbnail from S3 - key=%s, err=%v", memeToDelete.ThumbKey, err)
		} else {
			g.log.Infof("deleteMemeb: successfully deleted thumbnail from S3 - key=%s", memeToDelete.ThumbKey)
		}
	} else {
		g.log.Warnf("deleteMemeb: ThumbKey is empty for meme %s", memeID)
	}

	// Remove from index
	g.log.Infof("deleteMemeb: removing meme from JSON index at position %d", memeIndex)
	idx.Items = append(idx.Items[:memeIndex], idx.Items[memeIndex+1:]...)
	idx.UpdatedAt = time.Now()
	g.log.Infof("deleteMemeb: meme removed from index, remaining items=%d", len(idx.Items))

	// Write updated memes.json with retry logic
	maxRetries := 3
	var lastErr error
	for retry := 0; retry < maxRetries; retry++ {
		g.log.Infof("deleteMemeb: attempt %d/%d to update memes.json", retry+1, maxRetries)
		if err := g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &idx); err != nil {
			lastErr = err
			g.log.Errorf("deleteMemeb: attempt %d/%d FAILED to update memes.json - err=%v", retry+1, maxRetries, err)
			time.Sleep(time.Duration(retry+1) * 500 * time.Millisecond)
			continue
		}
		g.log.Infof("deleteMemeb: ✓ SUCCESS - meme deleted: %s (remaining: %d)", memeID, len(idx.Items))
		return nil
	}

	// If we get here, all retries failed
	g.log.Errorf("deleteMemeb: CRITICAL - failed to update memes.json after %d attempts - last error=%v", maxRetries, lastErr)
	return fmt.Errorf("failed to update memes.json: %w", lastErr)
}

// SyncWithS3 synchronizes memes.json with actual files in S3 memes/ folder
func (g *Generator) SyncWithS3(ctx context.Context) error {
	g.log.Infof("memes: starting sync with S3 folder")

	// Read current index
	var memesIdx model.MemesIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &memesIdx)
	if err != nil {
		return fmt.Errorf("read memes.json: %w", err)
	}
	if !found {
		memesIdx = model.MemesIndex{Items: []model.Meme{}}
	}

	// List all files in memes/ folder
	objects, err := g.s3.List(ctx, g.cfg.MemesPrefix)
	if err != nil {
		return fmt.Errorf("list S3 memes: %w", err)
	}

	// Create maps for tracking
	existingVideoKeys := make(map[string]bool)
	existingThumbKeys := make(map[string]bool)
	for _, item := range memesIdx.Items {
		if item.VideoKey != "" {
			existingVideoKeys[item.VideoKey] = true
		}
		if item.ThumbKey != "" {
			existingThumbKeys[item.ThumbKey] = true
		}
	}

	// Create map of actual keys in S3
	actualKeys := make(map[string]bool)
	for _, obj := range objects {
		actualKeys[obj.Key] = true
	}

	// Remove entries from JSON where video or thumbnail don't exist in S3
	originalCount := len(memesIdx.Items)
	filtered := make([]model.Meme, 0)
	for _, item := range memesIdx.Items {
		videoExists := actualKeys[item.VideoKey]
		thumbExists := actualKeys[item.ThumbKey]

		if videoExists && thumbExists {
			filtered = append(filtered, item)
		} else {
			g.log.Infof("memes: removing orphaned entry from JSON: %s (video: %v, thumb: %v)",
				item.ID, videoExists, thumbExists)
		}
	}
	memesIdx.Items = filtered

	removedCount := originalCount - len(filtered)
	if removedCount > 0 {
		g.log.Infof("memes: removed %d orphaned entries from JSON", removedCount)
	}

	// Delete orphaned files in S3 that are not tracked in JSON
	orphanedFiles := 0
	deletedFiles := 0
	for key := range actualKeys {
		if !existingVideoKeys[key] && !existingThumbKeys[key] {
			orphanedFiles++
			g.log.Infof("memes: deleting orphaned file from S3: %s", key)
			if err := g.s3.Delete(ctx, key); err != nil {
				g.log.Errorf("memes: failed to delete orphaned file %s: %v", key, err)
			} else {
				deletedFiles++
			}
		}
	}
	if orphanedFiles > 0 {
		g.log.Infof("memes: deleted %d/%d orphaned files from S3", deletedFiles, orphanedFiles)
	}

	// Update JSON
	memesIdx.UpdatedAt = time.Now()
	if err := g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &memesIdx); err != nil {
		return fmt.Errorf("write memes.json: %w", err)
	}

	g.log.Infof("memes: sync complete - JSON entries: %d, S3 files: %d, removed: %d, orphaned: %d",
		len(memesIdx.Items), len(objects), removedCount, orphanedFiles)
	return nil
}

// safeLoadVideo wraps moviego.Load to catch panics from the library
func safeLoadVideo(path string) (vid moviego.Video, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("moviego.Load panicked: %v", r)
		}
	}()
	vid, err = moviego.Load(path)
	return
}

// createVideoFromImageAndAudio creates a video from a static image and audio file using ffmpeg
func createVideoFromImageAndAudio(imagePath, audioPath, outputPath string, log *logging.Logger) error {
	// Get audio duration to determine video length
	probeCmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", audioPath)
	durationBytes, err := probeCmd.Output()
	if err != nil {
		log.Infof("ffprobe failed, using default duration: %v", err)
		// Default to 10 seconds if we can't determine audio duration
		return createVideoWithDuration(imagePath, audioPath, outputPath, 10)
	}

	var duration float64
	if _, err := fmt.Sscanf(string(durationBytes), "%f", &duration); err != nil || duration <= 0 {
		duration = 10
	}

	// Clamp duration between 8 and 12 seconds
	if duration < 8 {
		duration = 8
	} else if duration > 12 {
		duration = 12
	}

	return createVideoWithDuration(imagePath, audioPath, outputPath, duration)
}

// createVideoWithDuration creates video with specific duration
func createVideoWithDuration(imagePath, audioPath, outputPath string, duration float64) error {
	// Use ffmpeg to create video from image with audio
	// -loop 1: loop the image
	// -i image: input image
	// -i audio: input audio
	// -c:v libx264: video codec
	// -tune stillimage: optimize for static image
	// -c:a aac: audio codec
	// -b:a 192k: audio bitrate
	// -pix_fmt yuv420p: pixel format for compatibility
	// -shortest: finish when shortest input ends
	// -t duration: limit output duration
	cmd := exec.Command("ffmpeg",
		"-loop", "1",
		"-i", imagePath,
		"-i", audioPath,
		"-c:v", "libx264",
		"-tune", "stillimage",
		"-c:a", "aac",
		"-b:a", "192k",
		"-pix_fmt", "yuv420p",
		"-vf", "scale=1080:1920:force_original_aspect_ratio=decrease,pad=1080:1920:(ow-iw)/2:(oh-ih)/2:black",
		"-r", "30",
		"-shortest",
		"-t", fmt.Sprintf("%.2f", duration),
		"-y",
		outputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %w (output: %s)", err, string(output))
	}

	return nil
}

// extractThumbnail extracts a single frame from video at 1 second mark as thumbnail
func extractThumbnail(videoPath, outputPath string, log *logging.Logger) error {
	cmd := exec.Command("ffmpeg",
		"-i", videoPath,
		"-ss", "1",
		"-vframes", "1",
		"-q:v", "2",
		"-y",
		outputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Infof("ffmpeg thumbnail extraction failed: %v (output: %s)", err, string(output))
		return fmt.Errorf("extract thumbnail: %w", err)
	}

	return nil
}
