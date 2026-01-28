package video

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	cfg        internal.Config
	s3         s3.Client
	log        *logging.Logger
	audioIdx   *audio.Indexer
	sourcesScr *sources.Scraper
}

func NewGenerator(cfg internal.Config, s3c s3.Client, log *logging.Logger, audioIdx *audio.Indexer, sourcesScr *sources.Scraper) *Generator {
	return &Generator{cfg: cfg, s3: s3c, log: log, audioIdx: audioIdx, sourcesScr: sourcesScr}
}

func (g *Generator) EnsureMemes(ctx context.Context) error {
	g.log.Infof("video: ensuring memes index")
	var memesIdx model.MemesIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &memesIdx)
	if err != nil {
		return fmt.Errorf("read memes.json: %w", err)
	}
	if !found {
		memesIdx = model.MemesIndex{Items: []model.Meme{}}
	}

	if len(memesIdx.Items) >= g.cfg.MaxMemes {
		g.log.Infof("video: already at max memes (%d)", len(memesIdx.Items))
		memesIdx.UpdatedAt = time.Now()
		_ = g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &memesIdx)
		return nil
	}

	needed := g.cfg.MaxMemes - len(memesIdx.Items)
	for i := 0; i < needed; i++ {
		meme, err := g.generateOne(ctx, &memesIdx)
		if err != nil {
			g.log.Errorf("generate meme: %v", err)
			continue
		}
		memesIdx.Items = append(memesIdx.Items, *meme)
	}

	if len(memesIdx.Items) > g.cfg.MaxMemes {
		sorted := sortMemesByCreated(memesIdx.Items, false)
		toDelete := sorted[g.cfg.MaxMemes:]
		for _, m := range toDelete {
			_ = g.s3.Delete(ctx, m.VideoKey)
			_ = g.s3.Delete(ctx, m.ThumbKey)
		}
		memesIdx.Items = sorted[:g.cfg.MaxMemes]
		g.log.Infof("video: trimmed %d old memes", len(toDelete))
	}

	memesIdx.UpdatedAt = time.Now()
	if err := g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &memesIdx); err != nil {
		return fmt.Errorf("write memes.json: %w", err)
	}
	g.log.Infof("video: memes updated (total=%d)", len(memesIdx.Items))
	return nil
}

// GenerateOneMeme generates a single meme and returns it
func (g *Generator) GenerateOneMeme(ctx context.Context) (*model.Meme, error) {
	memesIdx := model.MemesIndex{Items: []model.Meme{}}
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &memesIdx)
	if err != nil {
		g.log.Errorf("read memes.json: %v", err)
		memesIdx = model.MemesIndex{Items: []model.Meme{}}
	}
	if !found {
		memesIdx = model.MemesIndex{Items: []model.Meme{}}
	}
	meme, err := g.generateOne(ctx, &memesIdx)
	if err != nil {
		return nil, err
	}

	// Update memes.json with new meme
	memesIdx.Items = append(memesIdx.Items, *meme)
	memesIdx.UpdatedAt = time.Now()
	if err := g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &memesIdx); err != nil {
		g.log.Errorf("failed to update memes.json: %v", err)
		return meme, fmt.Errorf("update memes.json: %w", err)
	}

	return meme, nil
}

func (g *Generator) generateOne(ctx context.Context, memesIdx *model.MemesIndex) (*model.Meme, error) {
	song, err := g.audioIdx.GetRandomSong(ctx)
	if err != nil {
		return nil, fmt.Errorf("get song: %w", err)
	}
	source, err := g.sourcesScr.GetRandomUnusedSource(ctx)
	if err != nil {
		return nil, fmt.Errorf("get source: %w", err)
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

	if err := g.s3.PutBytes(ctx, videoKey, videoData, "video/mp4"); err != nil {
		return nil, fmt.Errorf("upload video: %w", err)
	}

	// Extract thumbnail from video
	thumbPath := filepath.Join(os.TempDir(), fmt.Sprintf("thumb-%d.jpg", time.Now().UnixNano()))
	defer os.Remove(thumbPath)
	if err := extractThumbnail(videoPath, thumbPath, g.log); err != nil {
		g.log.Infof("failed to extract thumbnail: %v, using placeholder", err)
		_ = g.s3.PutBytes(ctx, thumbKey, []byte{}, "image/jpeg")
	} else {
		thumbData, err := os.ReadFile(thumbPath)
		if err != nil {
			g.log.Infof("failed to read thumbnail: %v", err)
		} else {
			_ = g.s3.PutBytes(ctx, thumbKey, thumbData, "image/jpeg")
		}
	}

	if err := g.sourcesScr.MarkSourceUsed(ctx, source.ID); err != nil {
		g.log.Errorf("mark source used: %v", err)
	}

	// Delete source file from S3
	if err := g.s3.Delete(ctx, source.MediaKey); err != nil {
		g.log.Errorf("failed to delete source %s from S3: %v", source.ID, err)
	}

	title := fmt.Sprintf("%s — %s", song.Title, source.Kind)

	return &model.Meme{
		ID:        memeID,
		Title:     title,
		VideoKey:  videoKey,
		ThumbKey:  thumbKey,
		SongID:    song.ID,
		SourceID:  source.ID,
		CreatedAt: time.Now(),
		SHA256:    hash,
	}, nil
}

func (g *Generator) memeExists(idx model.MemesIndex, sha256 string) bool {
	return lo.ContainsBy(idx.Items, func(m model.Meme) bool { return m.SHA256 == sha256 })
}

func (g *Generator) GetRandomMeme(ctx context.Context) (*model.Meme, error) {
	var idx model.MemesIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &idx)
	if err != nil || !found || len(idx.Items) == 0 {
		return nil, fmt.Errorf("no memes available")
	}
	i := randomIndex(len(idx.Items))
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
