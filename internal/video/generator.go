package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// ffmpegSem limits the number of concurrent ffmpeg processes to 1 to avoid
// "pthread_create() failed: Resource temporarily unavailable" under heavy load.
var ffmpegSem = make(chan struct{}, 1)

type Generator struct {
	cfg          internal.Config
	s3           s3.Client
	log          *logging.Logger
	audioIdx     *audio.Indexer
	sourcesScr   *sources.Scraper
	memesJSONMux sync.Mutex // Protects concurrent access to memes.json

	// Video hash blacklist in-memory cache (5-minute TTL)
	videoHashCacheMux     sync.RWMutex
	videoHashBlacklist    *model.VideoHashIndex
	videoHashBlacklistExp time.Time

	// Disliked sources in-memory cache (5-minute TTL)
	dislikedCacheMux sync.RWMutex
	dislikedCache    *model.DislikedSourceIndex
	dislikedCacheExp time.Time
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

		// Double check for visual duplicates before adding
		if meme.ImageHash != 0 && g.memeExistsByImageHash(memesIdx, meme.ImageHash) {
			g.log.Warnf("video: visual duplicate detected for meme %s (ImageHash already exists), skipping", meme.ID)
			continue
		}

		// Check against blacklist of historical video hashes
		if meme.ImageHash != 0 {
			inBlacklist, err := g.IsVideoHashInBlacklist(ctx, meme.ImageHash)
			if err != nil {
				g.log.Warnf("video: failed to check blacklist: %v", err)
			} else if inBlacklist {
				g.log.Warnf("video: blacklisted visual duplicate for meme %s (ImageHash in history), skipping", meme.ID)
				continue
			}
		}

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

			// Add hash to blacklist so we never re-generate this meme
			if m.ImageHash != 0 {
				if err := g.AddVideoHashToBlacklist(ctx, m.ImageHash); err != nil {
					g.log.Warnf("video: failed to add hash to blacklist for %s: %v", m.ID, err)
				}
			}
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

	// Try up to 10 times to find a valid source and create video
	var source *model.SourceAsset
	var sourcePath, audioPath, videoPath string
	var videoCreated bool

	// Load sources index ONCE before the retry loop to avoid N×ReadJSON(sources.json).
	sourcesIdx, err := g.sourcesScr.LoadSourcesIndex(ctx)
	if err != nil {
		return nil, fmt.Errorf("load sources index: %w", err)
	}
	triedSourceIDs := make(map[string]bool)

	for attempt := 0; attempt < 10; attempt++ {
		g.log.Infof("video: attempt %d/10 to create meme", attempt+1)

		// PickRandomUnused works on the in-memory index — no extra S3 read per attempt.
		src, err := g.sourcesScr.PickRandomUnused(ctx, &sourcesIdx, triedSourceIDs)
		if err != nil {
			return nil, fmt.Errorf("get source: %w", err)
		}
		triedSourceIDs[src.ID] = true
		g.log.Infof("video: got random source %s (key=%s)", src.ID, src.MediaKey)

		// Check if source is disliked (avoid recently disliked sources)
		isDisliked, err := g.IsSourceDisliked(ctx, src.ID)
		if err != nil {
			g.log.Warnf("video: failed to check if source is disliked: %v", err)
		}
		if isDisliked {
			g.log.Infof("video: skipping disliked source %s", src.ID)
			continue
		}

		// Check if file exists in S3 — uses HeadObject, no data transfer.
		g.log.Infof("video: checking if source exists in S3...")
		exists := false
		{
			checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			exists = g.sourcesScr.SourceExistsInS3(checkCtx, src)
			cancel()
		}

		if !exists {
			g.log.Warnf("source %s file not found in S3 (%s), removing from index", src.ID, src.MediaKey)
			if err := g.sourcesScr.RemoveSourceFromIndex(ctx, src.ID); err != nil {
				g.log.Errorf("failed to remove source from index: %v", err)
			}
			continue
		}

		g.log.Infof("video: ✓ source %s exists in S3, using it", src.ID)
		source = src

		// Download audio
		g.log.Infof("video: downloading audio for song %s...", song.ID)
		audioPath, err = g.audioIdx.DownloadSongToTemp(ctx, song)
		if err != nil {
			return nil, fmt.Errorf("download song: %w", err)
		}
		g.log.Infof("video: ✓ downloaded audio to %s", audioPath)

		// Download source
		g.log.Infof("video: downloading source %s from S3...", source.ID)
		sourcePath, err = g.sourcesScr.DownloadSourceToTemp(ctx, source)
		if err != nil {
			g.log.Warnf("video: failed to download source %s: %v, removing from index", source.ID, err)
			_ = g.sourcesScr.RemoveSourceFromIndex(ctx, source.ID)
			_ = g.s3.Delete(ctx, source.MediaKey)
			os.Remove(audioPath)
			continue
		}
		g.log.Infof("video: ✓ downloaded source to %s", sourcePath)

		// Validate source file exists and has reasonable size
		g.log.Infof("video: validating source file %s...", sourcePath)
		srcInfo, err := os.Stat(sourcePath)
		if err != nil {
			g.log.Warnf("video: source file stat failed: %v", err)
			os.Remove(sourcePath)
			os.Remove(audioPath)
			continue
		}
		if srcInfo.Size() < 1024 {
			g.log.Warnf("source file too small: %d bytes, skipping", srcInfo.Size())
			os.Remove(sourcePath)
			os.Remove(audioPath)
			continue
		}
		g.log.Infof("video: ✓ source file is valid (%d bytes)", srcInfo.Size())

		videoPath = filepath.Join(os.TempDir(), fmt.Sprintf("meme-%d.mp4", time.Now().UnixNano()))
		g.log.Infof("video: creating video at %s from image+audio...", videoPath)

		// Create video from image + audio using ffmpeg directly
		if err := createVideoFromImageAndAudio(ctx, sourcePath, audioPath, videoPath, g.log); err != nil {
			g.log.Warnf("video: failed to create video from %s + audio: %v", sourcePath, err)

			// If error contains "Invalid PNG" or similar, delete corrupted source
			errStr := err.Error()
			if strings.Contains(errStr, "Invalid PNG") || strings.Contains(errStr, "Invalid data found") ||
				strings.Contains(errStr, "Error submitting packet") {
				g.log.Warnf("video: detected corrupted source %s, deleting from S3 and index", source.ID)
				_ = g.sourcesScr.RemoveSourceFromIndex(ctx, source.ID)
				_ = g.s3.Delete(ctx, source.MediaKey)
			}

			os.Remove(sourcePath)
			os.Remove(audioPath)
			os.Remove(videoPath)
			continue
		}

		g.log.Infof("video: ✓ ffmpeg completed successfully, video created at %s", videoPath)
		videoCreated = true
		break
	}

	if !videoCreated || source == nil {
		return nil, fmt.Errorf("failed to create video after 10 attempts")
	}

	defer os.Remove(audioPath)
	defer os.Remove(sourcePath)
	defer os.Remove(videoPath)

	g.log.Infof("video: reading video file from %s...", videoPath)
	videoData, err := os.ReadFile(videoPath)
	if err != nil {
		return nil, err
	}
	g.log.Infof("video: ✓ read video file %s (%d bytes)", videoPath, len(videoData))

	g.log.Infof("video: computing SHA256 hash...")
	h := sha256.Sum256(videoData)
	hash := hex.EncodeToString(h[:])
	g.log.Infof("video: ✓ computed SHA256: %s", hash)

	if g.memeExists(*memesIdx, hash) {
		g.log.Warnf("video: duplicate meme detected (SHA256: %s), skipping", hash)
		return nil, fmt.Errorf("duplicate sha256")
	}

	memeID := fmt.Sprintf("meme-%d", time.Now().UnixNano())
	videoKey := g.cfg.MemesPrefix + memeID + ".mp4"
	thumbKey := g.cfg.MemesPrefix + memeID + "_thumb.jpg"

	// Upload video to S3 - CRITICAL step
	g.log.Infof("video: [S3 UPLOAD START] uploading video to S3 (key=%s, size=%d bytes)...", videoKey, len(videoData))
	err = g.s3.PutBytes(ctx, videoKey, videoData, "video/mp4")

	if err != nil {
		g.log.Errorf("CRITICAL: [S3 UPLOAD FAILED] failed to upload video to S3: %v", err)
		return nil, fmt.Errorf("upload video: %w", err)
	}

	g.log.Infof("video: [S3 UPLOAD SUCCESS] ✓ successfully uploaded video to S3: %s (%d bytes)", videoKey, len(videoData))

	// Use source image as thumbnail (simpler and more reliable than extracting from video)
	g.log.Infof("video: using source image as thumbnail...")
	if thumbData, err := os.ReadFile(sourcePath); err != nil {
		g.log.Warnf("video: failed to read source image as thumbnail: %v", err)
		_ = g.s3.PutBytes(ctx, thumbKey, []byte{}, "image/jpeg")
	} else {
		if err := g.s3.PutBytes(ctx, thumbKey, thumbData, "image/jpeg"); err != nil {
			g.log.Warnf("video: failed to upload thumbnail: %v", err)
		} else {
			g.log.Infof("video: ✓ thumbnail uploaded to S3: %s (%d bytes)", thumbKey, len(thumbData))
		}
	}

	// Mark source as used (non-critical)
	g.log.Infof("video: marking source %s as used...", source.ID)
	if err := g.sourcesScr.MarkSourceUsed(ctx, source.ID); err != nil {
		g.log.Warnf("video: failed to mark source as used: %v", err)
	} else {
		g.log.Infof("video: ✓ source marked as used")
	}

	// Delete source file from S3 (non-critical)
	g.log.Infof("video: deleting source file from S3: %s", source.MediaKey)
	if err := g.s3.Delete(ctx, source.MediaKey); err != nil {
		g.log.Warnf("video: failed to delete source from S3: %v", err)
	} else {
		g.log.Infof("video: ✓ source deleted from S3")
	}

	// Clean up author name by removing " - Topic" suffix that YouTube adds to official audio channels
	author := strings.TrimSuffix(song.Author, " - Topic")
	title := fmt.Sprintf("%s — %s", author, song.Title)

	// Compute image hash for thumbnail (visual uniqueness of memes)
	var imageHash uint64
	if thumbData, err := os.ReadFile(sourcePath); err == nil {
		if hash, err := g.sourcesScr.ComputeImageHash(thumbData); err != nil {
			g.log.Warnf("video: failed to compute image hash for thumbnail: %v", err)
		} else {
			imageHash = hash
			g.log.Infof("video: ✓ computed ImageHash for thumbnail: %d", imageHash)
		}
	}

	meme := &model.Meme{
		ID:        memeID,
		Title:     title,
		VideoKey:  videoKey,
		ThumbKey:  thumbKey,
		SongID:    song.ID,
		SourceID:  source.ID,
		CreatedAt: time.Now(),
		SHA256:    hash,
		ImageHash: imageHash,
	}

	g.log.Infof("video: meme created successfully: ID=%s, VideoKey=%s, ThumbKey=%s",
		meme.ID, meme.VideoKey, meme.ThumbKey)

	return meme, nil
}

func (g *Generator) memeExists(idx model.MemesIndex, sha256 string) bool {
	return lo.ContainsBy(idx.Items, func(m model.Meme) bool { return m.SHA256 == sha256 })
}

func (g *Generator) memeExistsByImageHash(idx model.MemesIndex, imageHash uint64) bool {
	return lo.ContainsBy(idx.Items, func(m model.Meme) bool { return m.ImageHash == imageHash && imageHash != 0 })
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

func (g *Generator) GetRandomMemes(ctx context.Context, count int) ([]*model.Meme, error) {
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

	// Clamp count to available memes
	if count > len(idx.Items) {
		count = len(idx.Items)
	}

	// Get unique random indices
	used := make(map[int]bool)
	result := make([]*model.Meme, 0, count)

	for i := 0; i < count; i++ {
		var randIdx int
		// Keep generating random index until we get a unique one
		for {
			randIdx = randomIndex(len(idx.Items))
			if !used[randIdx] {
				used[randIdx] = true
				break
			}
		}
		result = append(result, &idx.Items[randIdx])
	}

	g.log.Infof("returning %d unique random memes (total=%d)", len(result), len(idx.Items))
	return result, nil
}

func (g *Generator) DownloadMemeToTemp(ctx context.Context, meme *model.Meme) (string, error) {
	// Stream directly from S3 to avoid loading full video (5-20 MB) into heap.
	reader, err := g.s3.GetReader(ctx, meme.VideoKey)
	if err != nil {
		return "", err
	}
	defer reader.Reader.Close()
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("meme-%s.mp4", meme.ID))
	f, err := os.Create(tmpFile)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, reader.Reader); err != nil {
		os.Remove(tmpFile)
		return "", fmt.Errorf("stream meme from S3: %w", err)
	}
	return tmpFile, nil
}

// ReplaceAudioInMeme replaces the audio track in an existing meme with a new random audio
func (g *Generator) ReplaceAudioInMeme(ctx context.Context, memeID string) (*model.Meme, error) {
	g.log.Infof("ReplaceAudioInMeme: START - attempting to replace audio in memeID=%s", memeID)

	// Lock to prevent concurrent modifications
	g.memesJSONMux.Lock()

	// Read memes.json
	var idx model.MemesIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &idx)
	if err != nil {
		g.log.Errorf("ReplaceAudioInMeme: failed to read memes.json - err=%v", err)
		g.memesJSONMux.Unlock()
		return nil, fmt.Errorf("failed to read memes.json: %w", err)
	}
	if !found {
		g.log.Warnf("ReplaceAudioInMeme: memes.json not found in S3")
		g.memesJSONMux.Unlock()
		return nil, fmt.Errorf("memes.json not found")
	}

	// Find the meme
	memeIndex := -1
	var oldMeme *model.Meme
	for i, m := range idx.Items {
		if m.ID == memeID {
			memeIndex = i
			oldMeme = &idx.Items[i]
			g.log.Infof("ReplaceAudioInMeme: found meme at index %d", i)
			break
		}
	}

	if memeIndex == -1 {
		g.log.Errorf("ReplaceAudioInMeme: meme not found in JSON: %s", memeID)
		g.memesJSONMux.Unlock()
		return nil, fmt.Errorf("meme not found")
	}

	g.log.Infof("ReplaceAudioInMeme: found meme - ID=%s, VideoKey=%s, old SongID=%s",
		oldMeme.ID, oldMeme.VideoKey, oldMeme.SongID)

	// Unlock before starting long-running operations
	g.memesJSONMux.Unlock()

	// Get a new random audio track
	g.log.Infof("ReplaceAudioInMeme: selecting new random audio track...")
	newSong, err := g.audioIdx.GetRandomSong(ctx)
	if err != nil {
		g.log.Errorf("ReplaceAudioInMeme: failed to get random song: %v", err)
		return nil, fmt.Errorf("get song: %w", err)
	}
	g.log.Infof("ReplaceAudioInMeme: got new song %s (%s - %s)", newSong.ID, newSong.Author, newSong.Title)

	// Download the existing video from S3
	g.log.Infof("ReplaceAudioInMeme: downloading existing video from S3 (key=%s)...", oldMeme.VideoKey)
	videoData, _, err := g.s3.GetBytes(ctx, oldMeme.VideoKey)
	if err != nil {
		g.log.Errorf("ReplaceAudioInMeme: failed to download video from S3: %v", err)
		return nil, fmt.Errorf("download video: %w", err)
	}
	g.log.Infof("ReplaceAudioInMeme: ✓ downloaded video (%d bytes)", len(videoData))

	// Write video to temp file
	oldVideoPath := filepath.Join(os.TempDir(), fmt.Sprintf("meme-old-%d.mp4", time.Now().UnixNano()))
	if err := os.WriteFile(oldVideoPath, videoData, 0o644); err != nil {
		g.log.Errorf("ReplaceAudioInMeme: failed to write old video to temp: %v", err)
		return nil, fmt.Errorf("write video: %w", err)
	}
	defer os.Remove(oldVideoPath)
	g.log.Infof("ReplaceAudioInMeme: ✓ wrote old video to %s", oldVideoPath)

	// Download new audio
	g.log.Infof("ReplaceAudioInMeme: downloading audio for song %s...", newSong.ID)
	audioPath, err := g.audioIdx.DownloadSongToTemp(ctx, newSong)
	if err != nil {
		g.log.Errorf("ReplaceAudioInMeme: failed to download song: %v", err)
		return nil, fmt.Errorf("download song: %w", err)
	}
	defer os.Remove(audioPath)
	g.log.Infof("ReplaceAudioInMeme: ✓ downloaded audio to %s", audioPath)

	// Create new video by replacing audio in the old video
	newVideoPath := filepath.Join(os.TempDir(), fmt.Sprintf("meme-replace-%d.mp4", time.Now().UnixNano()))
	g.log.Infof("ReplaceAudioInMeme: replacing audio in video using ffmpeg...")

	if err := replaceAudioInVideo(ctx, oldVideoPath, audioPath, newVideoPath, g.log); err != nil {
		g.log.Errorf("ReplaceAudioInMeme: failed to replace audio in video: %v", err)
		os.Remove(newVideoPath)
		return nil, fmt.Errorf("replace audio: %w", err)
	}
	defer os.Remove(newVideoPath)
	g.log.Infof("ReplaceAudioInMeme: ✓ audio replaced successfully")

	// Read new video data
	newVideoData, err := os.ReadFile(newVideoPath)
	if err != nil {
		g.log.Errorf("ReplaceAudioInMeme: failed to read new video file: %v", err)
		return nil, fmt.Errorf("read video: %w", err)
	}
	g.log.Infof("ReplaceAudioInMeme: ✓ read new video file (%d bytes)", len(newVideoData))

	// Upload new video to S3, replacing the old one
	g.log.Infof("ReplaceAudioInMeme: uploading new video to S3 (key=%s)...", oldMeme.VideoKey)
	if err := g.s3.PutBytes(ctx, oldMeme.VideoKey, newVideoData, "video/mp4"); err != nil {
		g.log.Errorf("ReplaceAudioInMeme: failed to upload video to S3: %v", err)
		return nil, fmt.Errorf("upload video: %w", err)
	}
	g.log.Infof("ReplaceAudioInMeme: ✓ video uploaded to S3")

	// Re-acquire lock to update the memes index
	g.memesJSONMux.Lock()
	defer g.memesJSONMux.Unlock()

	// Re-read memes.json to get latest state
	found, err = g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &idx)
	if err != nil || !found {
		g.log.Errorf("ReplaceAudioInMeme: failed to re-read memes.json: %v", err)
		return nil, fmt.Errorf("re-read memes.json: %w", err)
	}

	// Find the meme again
	memeIndex = -1
	for i, m := range idx.Items {
		if m.ID == memeID {
			memeIndex = i
			break
		}
	}

	if memeIndex == -1 {
		g.log.Warnf("ReplaceAudioInMeme: meme not found in updated JSON: %s (might have been deleted)", memeID)
		return nil, fmt.Errorf("meme was deleted")
	}

	// Update the meme with new song information
	author := strings.TrimSuffix(newSong.Author, " - Topic")
	newTitle := fmt.Sprintf("%s — %s", author, newSong.Title)

	idx.Items[memeIndex].SongID = newSong.ID
	idx.Items[memeIndex].Title = newTitle
	idx.Items[memeIndex].CreatedAt = time.Now()
	idx.UpdatedAt = time.Now()

	g.log.Infof("ReplaceAudioInMeme: updating meme in index - old title=%s, new title=%s",
		oldMeme.Title, newTitle)

	// Write updated memes.json with retry logic
	maxRetries := 3
	var lastErr error
	for retry := 0; retry < maxRetries; retry++ {
		g.log.Infof("ReplaceAudioInMeme: attempt %d/%d to update memes.json", retry+1, maxRetries)
		if err := g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &idx); err != nil {
			lastErr = err
			g.log.Errorf("ReplaceAudioInMeme: attempt %d/%d FAILED to update memes.json - err=%v", retry+1, maxRetries, err)
			time.Sleep(time.Duration(retry+1) * 500 * time.Millisecond)
			continue
		}
		g.log.Infof("ReplaceAudioInMeme: ✓ SUCCESS - audio replaced: %s", memeID)
		return &idx.Items[memeIndex], nil
	}

	// If we get here, all retries failed
	g.log.Errorf("ReplaceAudioInMeme: CRITICAL - failed to update memes.json after %d attempts - last error=%v", maxRetries, lastErr)
	return nil, fmt.Errorf("failed to update memes.json: %w", lastErr)
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

	// Save the source ID before updating JSON (to delete it later)
	sourceIDToDelete := memeToDelete.SourceID

	// Add hash to blacklist so we never re-generate this meme
	if memeToDelete.ImageHash != 0 {
		if err := g.AddVideoHashToBlacklist(ctx, memeToDelete.ImageHash); err != nil {
			g.log.Warnf("deleteMemeb: failed to add hash to blacklist for %s: %v", memeID, err)
		}
	}

	// Add source to disliked blacklist to prevent immediate reuse
	if sourceIDToDelete != "" {
		if err := g.AddSourceToDislikedBlacklist(ctx, sourceIDToDelete); err != nil {
			g.log.Warnf("deleteMemeb: failed to blacklist disliked source %s: %v", sourceIDToDelete, err)
		}
	}

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
		g.log.Infof("deleteMemeb: ✓ memes.json updated successfully (remaining: %d)", len(idx.Items))

		// Successfully saved, now delete the source (after lock is released by defer)
		// We break here and handle source deletion outside the lock
		if sourceIDToDelete != "" {
			// Schedule source deletion after returning (will happen after defer unlock)
			// For now, return success and rely on best-effort source cleanup
			g.log.Infof("deleteMemeb: ✓ SUCCESS - meme fully deleted: %s (remaining: %d)", memeID, len(idx.Items))

			// Defer the source deletion until after this function returns
			go func() {
				g.log.Infof("deleteMemeb: async deleting associated source from index - SourceID=%s", sourceIDToDelete)
				if err := g.sourcesScr.RemoveSourceFromIndex(context.Background(), sourceIDToDelete); err != nil {
					g.log.Warnf("deleteMemeb: async failed to remove source %s from index: %v (source may already be deleted)", sourceIDToDelete, err)
				} else {
					g.log.Infof("deleteMemeb: async ✓ source %s removed from index", sourceIDToDelete)
				}
			}()
		}
		return nil
	}

	// If we get here, all retries failed
	g.log.Errorf("deleteMemeb: CRITICAL - failed to update memes.json after %d attempts - last error=%v", maxRetries, lastErr)
	return fmt.Errorf("failed to update memes.json: %w", lastErr)
}

// DeleteMemes deletes multiple memes in a single S3 read+write cycle (batch operation).
// This is more efficient than calling DeleteMeme N times (which does N reads + N writes).
func (g *Generator) DeleteMemes(ctx context.Context, memeIDs []string) error {
	if len(memeIDs) == 0 {
		return nil
	}
	g.log.Infof("DeleteMemes: batch deleting %d memes", len(memeIDs))

	g.memesJSONMux.Lock()
	defer g.memesJSONMux.Unlock()

	var idx model.MemesIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &idx)
	if err != nil {
		return fmt.Errorf("read memes.json: %w", err)
	}
	if !found {
		return nil
	}

	deleteSet := make(map[string]bool, len(memeIDs))
	for _, id := range memeIDs {
		deleteSet[id] = true
	}

	remaining := make([]model.Meme, 0, len(idx.Items))
	var toDelete []model.Meme
	for _, m := range idx.Items {
		if deleteSet[m.ID] {
			toDelete = append(toDelete, m)
		} else {
			remaining = append(remaining, m)
		}
	}

	idx.Items = remaining
	idx.UpdatedAt = time.Now()

	if err := g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &idx); err != nil {
		return fmt.Errorf("write memes.json: %w", err)
	}

	// Delete S3 files and run side-effects outside the lock (already released by defer)
	go func() {
		bgCtx := context.Background()
		for _, m := range toDelete {
			if m.VideoKey != "" {
				_ = g.s3.Delete(bgCtx, m.VideoKey)
			}
			if m.ThumbKey != "" {
				_ = g.s3.Delete(bgCtx, m.ThumbKey)
			}
			if m.ImageHash != 0 {
				_ = g.AddVideoHashToBlacklist(bgCtx, m.ImageHash)
			}
			if m.SourceID != "" {
				_ = g.AddSourceToDislikedBlacklist(bgCtx, m.SourceID)
				_ = g.sourcesScr.RemoveSourceFromIndex(bgCtx, m.SourceID)
			}
		}
		g.log.Infof("DeleteMemes: cleaned up %d memes from S3", len(toDelete))
	}()

	g.log.Infof("DeleteMemes: successfully removed %d memes from index", len(toDelete))
	return nil
}

// SyncWithS3 synchronizes memes.json with actual files in S3 memes/ folder
// Also ensures all memes are unique by SHA256
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
	// Also remove duplicate memes by SHA256
	originalCount := len(memesIdx.Items)
	filtered := make([]model.Meme, 0)
	seenSHA256 := make(map[string]bool)
	duplicateCount := 0

	for _, item := range memesIdx.Items {
		videoExists := actualKeys[item.VideoKey]
		thumbExists := actualKeys[item.ThumbKey]

		// Check if files exist in S3
		if !videoExists || !thumbExists {
			g.log.Infof("memes: removing orphaned entry from JSON: %s (video: %v, thumb: %v)",
				item.ID, videoExists, thumbExists)
			continue
		}

		// Check for SHA256 duplicates
		if item.SHA256 != "" && seenSHA256[item.SHA256] {
			g.log.Warnf("memes: removing duplicate meme detected (SHA256: %s, id: %s, existing id will be kept)",
				item.SHA256, item.ID)
			// Delete the duplicate video and thumbnail from S3
			_ = g.s3.Delete(ctx, item.VideoKey)
			_ = g.s3.Delete(ctx, item.ThumbKey)
			duplicateCount++
			continue
		}

		if item.SHA256 != "" {
			seenSHA256[item.SHA256] = true
		}

		filtered = append(filtered, item)
	}

	memesIdx.Items = filtered

	removedCount := originalCount - len(filtered)
	if removedCount > 0 {
		g.log.Infof("memes: removed %d entries from JSON (orphaned: %d, duplicates: %d)",
			removedCount, removedCount-duplicateCount, duplicateCount)
	}

	// Update lists of existing keys based on filtered memes
	filteredVideoKeys := make(map[string]bool)
	filteredThumbKeys := make(map[string]bool)
	for _, item := range filtered {
		if item.VideoKey != "" {
			filteredVideoKeys[item.VideoKey] = true
		}
		if item.ThumbKey != "" {
			filteredThumbKeys[item.ThumbKey] = true
		}
	}

	// Delete orphaned files in S3 that are not tracked in filtered JSON
	orphanedFiles := 0
	deletedFiles := 0
	for key := range actualKeys {
		if !filteredVideoKeys[key] && !filteredThumbKeys[key] {
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

	g.log.Infof("memes: sync complete - JSON entries: %d, S3 files: %d, removed: %d (duplicates: %d), orphaned files deleted: %d",
		len(memesIdx.Items), len(objects), removedCount, duplicateCount, deletedFiles)
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
func createVideoFromImageAndAudio(ctx context.Context, imagePath, audioPath, outputPath string, log *logging.Logger) error {
	log.Infof("[FFMPEG] determining audio duration from %s...", audioPath)

	// Get audio duration to determine video length
	// Use context with timeout for ffprobe
	ctxProbe, cancelProbe := context.WithTimeout(ctx, 30*time.Second)
	defer cancelProbe()

	probeCmd := exec.CommandContext(ctxProbe, "ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", audioPath)
	durationBytes, err := probeCmd.Output()
	if err != nil {
		log.Infof("[FFMPEG] ffprobe failed (timeout?): %v", err)
		// Default to 10 seconds if we can't determine audio duration
		return createVideoWithDuration(ctx, imagePath, audioPath, outputPath, 10, log)
	}

	var duration float64
	if _, err := fmt.Sscanf(string(durationBytes), "%f", &duration); err != nil || duration <= 0 {
		log.Infof("[FFMPEG] failed to parse duration, using default")
		duration = 10
	}

	// Clamp duration between 8 and 12 seconds
	if duration < 8 {
		log.Infof("[FFMPEG] duration too short (%f), clamping to 8s", duration)
		duration = 8
	} else if duration > 12 {
		log.Infof("[FFMPEG] duration too long (%f), clamping to 12s", duration)
		duration = 12
	}

	log.Infof("[FFMPEG] ✓ determined audio duration: %.2f seconds", duration)
	return createVideoWithDuration(ctx, imagePath, audioPath, outputPath, duration, log)
}

// createVideoWithDuration creates video with specific duration
func createVideoWithDuration(ctx context.Context, imagePath, audioPath, outputPath string, duration float64, log *logging.Logger) error {
	log.Infof("[FFMPEG] starting ffmpeg with duration %.2f seconds", duration)
	log.Infof("[FFMPEG] image: %s", imagePath)
	log.Infof("[FFMPEG] audio: %s", audioPath)
	log.Infof("[FFMPEG] output: %s", outputPath)

	// Validate input files exist before running ffmpeg
	if _, err := os.Stat(imagePath); err != nil {
		return fmt.Errorf("image file not found: %s (%w)", imagePath, err)
	}
	if _, err := os.Stat(audioPath); err != nil {
		return fmt.Errorf("audio file not found: %s (%w)", audioPath, err)
	}

	// Use ffmpeg to create video from image with audio
	// Use -f lavfi to generate black background + scale+pad image on top (avoids -loop hanging)
	// This is more stable than using -loop 1 with images

	// ffmpeg filter: 1. Create black background 2. Scale image 3. Pad to 1080x1920 4. Overlay on background
	filterComplex := fmt.Sprintf(
		"[0:v]scale=1080:1920:force_original_aspect_ratio=decrease,pad=1080:1920:(ow-iw)/2:(oh-ih)/2:black[v];[v]setsar=1[out]",
	)

	// Acquire semaphore – only one ffmpeg process at a time to avoid exhausting system threads.
	ffmpegSem <- struct{}{}
	defer func() { <-ffmpegSem }()

	var stderr bytes.Buffer
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-threads", "1", // global thread limit
		"-filter_threads", "1", // scale/pad filter threads
		"-filter_complex_threads", "1", // filter_complex graph threads
		"-i", imagePath,
		"-i", audioPath,
		"-filter_complex", filterComplex,
		"-map", "[out]",
		"-map", "1:a",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "stillimage",
		"-x264-params", "threads=1", // libx264 internal thread pool
		"-c:a", "aac",
		"-b:a", "192k",
		"-pix_fmt", "yuv420p",
		"-r", "30",
		"-t", fmt.Sprintf("%.2f", duration),
		"-y",
		"-strict", "-2", // Allow experimental codecs
		outputPath,
	)
	cmd.Stderr = &stderr

	log.Infof("[FFMPEG] executing ffmpeg with filter_complex (duration=%.2fs)", duration)

	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		log.Errorf("[FFMPEG] ✗ ffmpeg failed (exit code: %v): %s", err, errMsg)
		return fmt.Errorf("ffmpeg error: %s", errMsg)
	}

	// Verify output file was created
	if _, err := os.Stat(outputPath); err != nil {
		return fmt.Errorf("ffmpeg did not create output file: %s (%w)", outputPath, err)
	}

	log.Infof("[FFMPEG] ✓ ffmpeg completed successfully, output file: %s", outputPath)
	return nil
}

// replaceAudioInVideo replaces the audio track in an existing video with a new audio file
func replaceAudioInVideo(ctx context.Context, videoPath, audioPath, outputPath string, log *logging.Logger) error {
	log.Infof("[FFMPEG] replacing audio in video")
	log.Infof("[FFMPEG] video: %s", videoPath)
	log.Infof("[FFMPEG] audio: %s", audioPath)
	log.Infof("[FFMPEG] output: %s", outputPath)

	// Get video duration
	log.Infof("[FFMPEG] determining video duration from %s...", videoPath)
	ctxProbe, cancelProbe := context.WithTimeout(ctx, 30*time.Second)
	defer cancelProbe()

	probeCmd := exec.CommandContext(ctxProbe, "ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", videoPath)
	durationBytes, err := probeCmd.Output()
	if err != nil {
		log.Infof("[FFMPEG] ffprobe failed (timeout?): %v", err)
		// Default to 10 seconds if we can't determine video duration
		return replaceAudioWithDuration(ctx, videoPath, audioPath, outputPath, 10, log)
	}

	var duration float64
	if _, err := fmt.Sscanf(string(durationBytes), "%f", &duration); err != nil || duration <= 0 {
		log.Infof("[FFMPEG] failed to parse duration, using default")
		duration = 10
	}

	log.Infof("[FFMPEG] ✓ determined video duration: %.2f seconds", duration)
	return replaceAudioWithDuration(ctx, videoPath, audioPath, outputPath, duration, log)
}

// replaceAudioWithDuration replaces audio and trims it to match video duration
func replaceAudioWithDuration(ctx context.Context, videoPath, audioPath, outputPath string, duration float64, log *logging.Logger) error {
	log.Infof("[FFMPEG] replacing audio (trimmed to %.2f seconds)", duration)

	// Validate input files exist
	if _, err := os.Stat(videoPath); err != nil {
		return fmt.Errorf("video file not found: %s (%w)", videoPath, err)
	}
	if _, err := os.Stat(audioPath); err != nil {
		return fmt.Errorf("audio file not found: %s (%w)", audioPath, err)
	}

	// Use ffmpeg to replace audio in video with trimmed audio
	// -i video_input -i audio_input -c:v copy -c:a aac -map 0:v:0 -map 1:a:0 -t duration output

	// Acquire semaphore – only one ffmpeg process at a time.
	ffmpegSem <- struct{}{}
	defer func() { <-ffmpegSem }()

	var stderr bytes.Buffer
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-threads", "1", // global thread limit
		"-filter_threads", "1", // filter graph threads
		"-filter_complex_threads", "1",
		"-i", videoPath,
		"-i", audioPath,
		"-c:v", "copy",
		"-c:a", "aac",
		"-b:a", "192k",
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-t", fmt.Sprintf("%.2f", duration),
		"-y",
		"-strict", "-2",
		outputPath,
	)
	cmd.Stderr = &stderr

	log.Infof("[FFMPEG] executing ffmpeg to replace and trim audio (duration=%.2fs)", duration)

	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		log.Errorf("[FFMPEG] ✗ ffmpeg failed (exit code: %v): %s", err, errMsg)
		return fmt.Errorf("ffmpeg error: %s", errMsg)
	}

	// Verify output file was created
	if _, err := os.Stat(outputPath); err != nil {
		return fmt.Errorf("ffmpeg did not create output file: %s (%w)", outputPath, err)
	}

	log.Infof("[FFMPEG] ✓ audio replaced and trimmed successfully, output file: %s", outputPath)
	return nil
}
