package video

import (
	"context"
	"time"

	"github.com/samber/lo"

	"meme-video-gen/internal/model"
)

// IsVideoHashInBlacklist checks if a hash exists in the video hash blacklist.
// Uses an in-memory cache with 5-minute TTL to avoid hitting S3 per generation attempt.
func (g *Generator) IsVideoHashInBlacklist(ctx context.Context, hash uint64) (bool, error) {
	if hash == 0 {
		return false, nil
	}

	g.videoHashCacheMux.RLock()
	cached := g.videoHashBlacklist
	exp := g.videoHashBlacklistExp
	g.videoHashCacheMux.RUnlock()

	if cached == nil || time.Now().After(exp) {
		var index model.VideoHashIndex
		found, err := g.s3.ReadJSON(ctx, g.cfg.VideoHashIndexKey, &index)
		if err != nil {
			g.log.Warnf("video_hash: failed to read blacklist: %v", err)
			return false, nil
		}
		if !found {
			index = model.VideoHashIndex{Hashes: []uint64{}}
		}
		g.videoHashCacheMux.Lock()
		g.videoHashBlacklist = &index
		g.videoHashBlacklistExp = time.Now().Add(5 * time.Minute)
		g.videoHashCacheMux.Unlock()
		cached = &index
	}

	return lo.Contains(cached.Hashes, hash), nil
}

// AddVideoHashToBlacklist adds a hash to the video hash blacklist and invalidates the cache.
func (g *Generator) AddVideoHashToBlacklist(ctx context.Context, hash uint64) error {
	if hash == 0 {
		return nil
	}

	// Always load fresh from S3 to avoid overwriting concurrent additions.
	var index model.VideoHashIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.VideoHashIndexKey, &index)
	if err != nil {
		g.log.Errorf("video_hash: failed to read blacklist for update: %v", err)
		index = model.VideoHashIndex{Hashes: []uint64{}}
	}
	if !found {
		index = model.VideoHashIndex{Hashes: []uint64{}}
	}

	if !lo.Contains(index.Hashes, hash) {
		index.Hashes = append(index.Hashes, hash)
		g.log.Infof("video_hash: added hash %d to blacklist (total: %d)", hash, len(index.Hashes))
	}

	err = g.s3.WriteJSON(ctx, g.cfg.VideoHashIndexKey, &index)
	if err == nil {
		// Invalidate in-memory cache
		g.videoHashCacheMux.Lock()
		g.videoHashBlacklist = nil
		g.videoHashCacheMux.Unlock()
	}
	return err
}
