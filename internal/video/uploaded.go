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

	"meme-video-gen/internal/model"
)

// RegisterUploadedVideo stores a processed user video in the regular meme index.
// This lets the existing publish, delete, and change-audio callbacks work for it.
func (g *Generator) RegisterUploadedVideo(ctx context.Context, videoPath, title, songID string) (*model.Meme, error) {
	videoData, err := os.ReadFile(videoPath)
	if err != nil {
		return nil, fmt.Errorf("read uploaded video: %w", err)
	}
	if len(videoData) == 0 {
		return nil, fmt.Errorf("uploaded video is empty")
	}

	id := fmt.Sprintf("upload-%d", time.Now().UnixNano())
	videoKey := g.cfg.MemesPrefix + id + ".mp4"
	thumbKey := g.cfg.MemesPrefix + id + "_thumb.jpg"
	videoHash := sha256.Sum256(videoData)

	if err := g.s3.PutBytes(ctx, videoKey, videoData, "video/mp4"); err != nil {
		return nil, fmt.Errorf("upload video: %w", err)
	}
	cleanupVideo := true
	defer func() {
		if cleanupVideo {
			_ = g.s3.Delete(context.Background(), videoKey)
		}
	}()

	thumbPath := filepath.Join(os.TempDir(), fmt.Sprintf("%s-thumb.jpg", id))
	defer os.Remove(thumbPath)

	thumbData, thumbErr := createUploadedVideoThumbnail(ctx, videoPath, thumbPath)
	if thumbErr == nil {
		if err := g.s3.PutBytes(ctx, thumbKey, thumbData, "image/jpeg"); err != nil {
			g.log.Warnf("uploaded video: thumbnail upload failed: %v", err)
			thumbKey = ""
		}
	} else {
		g.log.Warnf("uploaded video: thumbnail creation failed: %v", thumbErr)
		thumbKey = ""
	}

	if title == "" {
		title = "Uploaded video"
	}
	meme := &model.Meme{
		ID:        id,
		Title:     title,
		VideoKey:  videoKey,
		ThumbKey:  thumbKey,
		SongID:    songID,
		CreatedAt: time.Now(),
		SHA256:    hex.EncodeToString(videoHash[:]),
	}

	g.memesJSONMux.Lock()
	defer g.memesJSONMux.Unlock()

	var index model.MemesIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &index)
	if err != nil {
		return nil, fmt.Errorf("read memes index: %w", err)
	}
	if !found {
		index.Items = []model.Meme{}
	}
	index.Items = append(index.Items, *meme)
	index.UpdatedAt = time.Now()
	if err := g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &index); err != nil {
		return nil, fmt.Errorf("write memes index: %w", err)
	}

	cleanupVideo = false
	return meme, nil
}

func createUploadedVideoThumbnail(ctx context.Context, videoPath, thumbPath string) ([]byte, error) {
	ffmpegSem <- struct{}{}
	defer func() { <-ffmpegSem }()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-i", videoPath,
		"-frames:v", "1",
		"-q:v", "2",
		"-y", thumbPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg thumbnail: %w: %s", err, output)
	}
	return os.ReadFile(thumbPath)
}
