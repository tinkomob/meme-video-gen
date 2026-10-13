package scheduler

import (
	"context"
	"time"

	"meme-video-gen/internal"
	"meme-video-gen/internal/s3"
)

const defaultMixtapesPerDay = 2

// BestOfEngagementConfig controls "Best of [Artist]" compilations.
type BestOfEngagementConfig struct {
	Enabled       bool      `json:"enabled"`
	Artists       []string  `json:"artists"`
	SegmentCount  int       `json:"segment_count"`
	IntervalDays  int       `json:"interval_days"`
	LastPostedAt  time.Time `json:"last_posted_at"`
	NextArtistIdx int       `json:"next_artist_idx"`
}

// TeaserEngagementConfig controls the daily "Wanna know this song?" teaser.
type TeaserEngagementConfig struct {
	Enabled      bool      `json:"enabled"`
	Hour         int       `json:"hour"`
	Minute       int       `json:"minute"`
	LastPostedAt time.Time `json:"last_posted_at"`
}

// MixtapeEngagementConfig holds engagement settings for Best Of and Teaser features.
type MixtapeEngagementConfig struct {
	Version int                    `json:"version"` // bumped when defaults change; triggers migration
	BestOf  BestOfEngagementConfig `json:"best_of"`
	Teaser  TeaserEngagementConfig `json:"teaser"`
}

const currentEngagementConfigVersion = 1

// DefaultEngagementConfig returns sensible defaults (both features enabled).
func DefaultEngagementConfig() *MixtapeEngagementConfig {
	return &MixtapeEngagementConfig{
		Version: currentEngagementConfigVersion,
		BestOf: BestOfEngagementConfig{
			Enabled:      true,
			Artists:      []string{"dee bill", "eenfinit"},
			SegmentCount: 5,
			IntervalDays: 3,
		},
		Teaser: TeaserEngagementConfig{
			Enabled: true,
			Hour:    18,
			Minute:  0,
		},
	}
}

// SaveEngagementConfig persists engagement config to S3.
func SaveEngagementConfig(ctx context.Context, client s3.Client, cfg *internal.Config, config *MixtapeEngagementConfig) error {
	return client.WriteJSON(ctx, cfg.EngagementConfigJSONKey, config)
}

// LoadEngagementConfig loads engagement config from S3; returns defaults if not found.
// Migrates old configs (version 0) to current defaults by enabling both features.
func LoadEngagementConfig(ctx context.Context, client s3.Client, cfg *internal.Config) (*MixtapeEngagementConfig, error) {
	var config MixtapeEngagementConfig
	found, err := client.ReadJSON(ctx, cfg.EngagementConfigJSONKey, &config)
	if err != nil {
		return nil, err
	}
	if !found {
		defaults := DefaultEngagementConfig()
		_ = client.WriteJSON(ctx, cfg.EngagementConfigJSONKey, defaults)
		return defaults, nil
	}
	// Migrate: version 0 configs predate the "enabled by default" change вЂ” enable both features.
	if config.Version < currentEngagementConfigVersion {
		config.Version = currentEngagementConfigVersion
		config.BestOf.Enabled = true
		config.Teaser.Enabled = true
		_ = client.WriteJSON(ctx, cfg.EngagementConfigJSONKey, &config)
	}
	return &config, nil
}
