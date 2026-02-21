package video

import (
	"context"
	"fmt"
	"time"

	"meme-video-gen/internal/model"
)

// AddSourceToDislikedBlacklist temporarily blacklists a source so it won't be reused
func (g *Generator) AddSourceToDislikedBlacklist(ctx context.Context, sourceID string) error {
	if sourceID == "" {
		return nil
	}

	// Load current blacklist
	var idx model.DislikedSourceIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.DislikedSourcesJSONKey, &idx)
	if err != nil && !found {
		// File doesn't exist yet, create new index
		idx = model.DislikedSourceIndex{
			UpdatedAt: time.Now(),
			Items:     []model.DislikedSource{},
		}
	} else if err != nil {
		g.log.Warnf("disliked: failed to read blacklist: %v", err)
		return fmt.Errorf("failed to read disliked sources: %w", err)
	}

	// Check if source is already blacklisted (don't add duplicates)
	for _, item := range idx.Items {
		if item.SourceID == sourceID {
			g.log.Infof("disliked: source %s already blacklisted, skipping", sourceID)
			return nil
		}
	}

	// Add source to blacklist with grace period
	gracePeriodSeconds := int64(g.cfg.DislikedSourceGracePeriod.Seconds())
	idx.Items = append(idx.Items, model.DislikedSource{
		SourceID:   sourceID,
		Duration:   gracePeriodSeconds,
		DislikedAt: time.Now(),
	})
	idx.UpdatedAt = time.Now()

	g.log.Infof("disliked: added source %s to blacklist for %.0f hours", sourceID, g.cfg.DislikedSourceGracePeriod.Hours())

	if err := g.s3.WriteJSON(ctx, g.cfg.DislikedSourcesJSONKey, &idx); err != nil {
		g.log.Errorf("disliked: failed to update blacklist: %v", err)
		return fmt.Errorf("failed to update disliked sources: %w", err)
	}

	return nil
}

// IsSourceDisliked checks if a source is currently blacklisted
func (g *Generator) IsSourceDisliked(ctx context.Context, sourceID string) (bool, error) {
	var idx model.DislikedSourceIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.DislikedSourcesJSONKey, &idx)
	if err != nil || !found {
		return false, nil // No blacklist or error reading it
	}

	return idx.IsBlacklisted(sourceID), nil
}

// CleanupDislikedSources removes expired entries from the blacklist
func (g *Generator) CleanupDislikedSources(ctx context.Context) error {
	var idx model.DislikedSourceIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.DislikedSourcesJSONKey, &idx)
	if err != nil || !found {
		return nil // No blacklist file, nothing to clean
	}

	initialCount := len(idx.Items)
	idx.CleanupExpired()

	if len(idx.Items) == initialCount {
		return nil // No changes needed
	}

	g.log.Infof("disliked: cleaning up expired entries (%d → %d)", initialCount, len(idx.Items))

	if len(idx.Items) == 0 {
		// If empty, we could delete the file or keep it - let's delete it
		if err := g.s3.Delete(ctx, g.cfg.DislikedSourcesJSONKey); err != nil {
			g.log.Warnf("disliked: failed to delete empty blacklist: %v", err)
		}
		return nil
	}

	idx.UpdatedAt = time.Now()
	if err := g.s3.WriteJSON(ctx, g.cfg.DislikedSourcesJSONKey, &idx); err != nil {
		g.log.Errorf("disliked: failed to save cleaned up blacklist: %v", err)
		return fmt.Errorf("failed to save disliked sources: %w", err)
	}

	return nil
}
