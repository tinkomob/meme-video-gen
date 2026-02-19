package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"meme-video-gen/internal/model"
)

func (sc *Scraper) downloadAsset(ctx context.Context, mediaURL string, kind model.SourceKind, sourceURL string) (*model.SourceAsset, error) {
	resp, err := http.Get(mediaURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http %d for %s", resp.StatusCode, mediaURL)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	h := sha256.Sum256(data)
	hash := hex.EncodeToString(h[:])

	// Compute perceptual image hash
	imageHash, err := sc.ComputeImageHash(data)
	if err != nil {
		sc.log.Warnf("image_hash: failed to compute hash for %s: %v (continuing anyway)", mediaURL, err)
		// Don't fail the entire download if hashing fails
		imageHash = 0
	}

	ext := ".jpg"
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "png") {
		ext = ".png"
	} else if strings.Contains(contentType, "gif") {
		ext = ".gif"
	} else if strings.Contains(contentType, "webp") {
		ext = ".webp"
	}

	id := fmt.Sprintf("%s-%d", kind, time.Now().UnixNano())
	key := sc.cfg.SourcesPrefix + id + ext

	if err := sc.s3.PutBytes(ctx, key, data, contentType); err != nil {
		return nil, fmt.Errorf("s3 upload: %w", err)
	}

	return &model.SourceAsset{
		ID:         id,
		Kind:       kind,
		SourceURL:  sourceURL,
		MediaKey:   key,
		MimeType:   contentType,
		AddedAt:    time.Now(),
		LastSeenAt: time.Now(),
		Used:       false,
		SHA256:     hash,
		ImageHash:  imageHash,
	}, nil
}
