package snippet

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"meme-video-gen/internal"
	"meme-video-gen/internal/audio"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/s3"
)

var ffmpegSem = make(chan struct{}, 1)

type Generator struct {
	cfg   internal.Config
	s3    s3.Client
	audio *audio.Indexer
	log   *logging.Logger
}

func NewGenerator(cfg internal.Config, s3c s3.Client, audioIdx *audio.Indexer, log *logging.Logger) *Generator {
	return &Generator{cfg: cfg, s3: s3c, audio: audioIdx, log: log}
}

// downloadThumbnail loads validated YouTube covers for song snippets.
func (g *Generator) downloadThumbnail(ctx context.Context, videoID, dir string) (string, error) {
	if len(videoID) != 11 || strings.Trim(videoID, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return "", fmt.Errorf("invalid YouTube video ID %q", videoID)
	}
	destPath := filepath.Join(dir, "cover.jpg")
	key := "covers/" + videoID + ".jpg"
	if data, _, err := g.s3.GetBytes(ctx, key); err == nil && validSongCover(data) {
		return destPath, os.WriteFile(destPath, data, 0600)
	}
	client := &http.Client{Timeout: 4 * time.Second}
	var lastErr error
	// HTTP is a fallback for public images when the local network blocks YouTube TLS.
	for _, host := range []string{"https://i.ytimg.com", "https://img.youtube.com", "http://i.ytimg.com"} {
		for _, size := range []string{"maxresdefault", "sddefault", "hqdefault"} {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			u := fmt.Sprintf("%s/vi/%s/%s.jpg", host, videoID, size)
			data, err := fetchSongCover(ctx, client, u)
			if err != nil {
				lastErr = err
				if _, ok := err.(*url.Error); ok {
					break
				}
				continue
			}
			if err := os.WriteFile(destPath, data, 0600); err != nil {
				return "", err
			}
			if err := g.s3.PutBytes(ctx, key, data, "image/jpeg"); err != nil {
				g.log.Warnf("could not cache song cover: %v", err)
			}
			g.log.Infof("song cover loaded: %s (%d bytes)", u, len(data))
			return destPath, nil
		}
	}
	return "", fmt.Errorf("could not download thumbnail for video %s: %w", videoID, lastErr)
}

func validSongCover(data []byte) bool {
	if len(data) > 8<<20 {
		return false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	return err == nil && format == "jpeg" && cfg.Width >= 300 && cfg.Height >= 170 && cfg.Width <= 8192 && cfg.Height <= 8192
}

func fetchSongCover(ctx context.Context, client *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("thumbnail HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return nil, err
	}
	if !validSongCover(data) {
		return nil, fmt.Errorf("thumbnail is invalid or a YouTube placeholder")
	}
	return data, nil
}

// escapeFfmpegPath escapes a file path for use inside a single-quoted FFmpeg filter option
// (e.g. textfile='...'). Only backslash and colon need escaping for typical temp paths.
func escapeFfmpegPath(p string) string {
	p = strings.ReplaceAll(p, `\`, `\\`)
	p = strings.ReplaceAll(p, `:`, `\:`)
	return p
}
