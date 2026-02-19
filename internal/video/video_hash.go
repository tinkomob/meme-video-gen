package video

import (
	"context"

	"github.com/samber/lo"

	"meme-video-gen/internal/model"
)

// IsVideoHashInBlacklist checks if a hash exists in the video hash blacklist
func (g *Generator) IsVideoHashInBlacklist(ctx context.Context, hash uint64) (bool, error) {
	if hash == 0 {
		return false, nil
	}

	var index model.VideoHashIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.VideoHashIndexKey, &index)
	if err != nil {
		g.log.Warnf("video_hash: failed to read blacklist: %v", err)
		return false, nil // Be permissive on read errors
	}
	if !found {
		return false, nil
	}

	return lo.Contains(index.Hashes, hash), nil
}

// AddVideoHashToBlacklist adds a hash to the video hash blacklist
func (g *Generator) AddVideoHashToBlacklist(ctx context.Context, hash uint64) error {
	if hash == 0 {
		return nil
	}

	var index model.VideoHashIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.VideoHashIndexKey, &index)
	if err != nil {
		g.log.Errorf("video_hash: failed to read blacklist for update: %v", err)
		index = model.VideoHashIndex{Hashes: []uint64{}}
	}
	if !found {
		index = model.VideoHashIndex{Hashes: []uint64{}}
	}

	// Only add if not already present
	if !lo.Contains(index.Hashes, hash) {
		index.Hashes = append(index.Hashes, hash)
		g.log.Infof("video_hash: added hash %d to blacklist (total: %d)", hash, len(index.Hashes))
	}

	return g.s3.WriteJSON(ctx, g.cfg.VideoHashIndexKey, &index)
}
