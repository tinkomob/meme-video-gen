package scheduler

import (
	"context"
	"math/rand"
	"sort"
	"time"

	"meme-video-gen/internal"
	"meme-video-gen/internal/s3"
)

const defaultMixtapesPerDay = 5

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
	// Migrate: version 0 configs predate the "enabled by default" change — enable both features.
	if config.Version < currentEngagementConfigVersion {
		config.Version = currentEngagementConfigVersion
		config.BestOf.Enabled = true
		config.Teaser.Enabled = true
		_ = client.WriteJSON(ctx, cfg.EngagementConfigJSONKey, &config)
	}
	return &config, nil
}

// MixtapeScheduleEntry represents a single scheduled mixtape send time.
type MixtapeScheduleEntry struct {
	Time time.Time `json:"time"`
}

// DailyMixtapeSchedule holds the mixtape schedule for a single day.
type DailyMixtapeSchedule struct {
	Date      string                 `json:"date"` // YYYY-MM-DD
	Entries   []MixtapeScheduleEntry `json:"entries"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// BuildDailyMixtapeSchedule picks count fully random times across the 24 h day
// ensuring at least minGapSeconds between consecutive posts.
func BuildDailyMixtapeSchedule(date time.Time, count int) []time.Time {
	const minGapSeconds = 7200 // 2 h minimum between posts

	loc := time.FixedZone("Asia/Tomsk", 7*3600)
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	end := time.Date(date.Year(), date.Month(), date.Day(), 23, 59, 59, 0, loc)

	totalSeconds := int(end.Sub(start).Seconds())
	if count <= 0 {
		return nil
	}
	if count == 1 {
		return []time.Time{start.Add(time.Duration(rand.Intn(totalSeconds+1)) * time.Second)}
	}

	// Compress the [0, totalSeconds] range by (count-1)*minGap so that after
	// spacing the sorted points apart by minGap every pair is at least minGap apart.
	available := totalSeconds - (count-1)*minGapSeconds
	if available < 0 {
		available = 0
	}

	points := make([]int, count)
	for i := range points {
		points[i] = rand.Intn(available + 1)
	}
	sort.Ints(points)

	times := make([]time.Time, count)
	for i, p := range points {
		seconds := p + i*minGapSeconds
		t := start.Add(time.Duration(seconds) * time.Second)
		if t.After(end) {
			t = end
		}
		times[i] = t
	}

	return times
}

// SaveMixtapeSchedule saves the mixtape schedule to S3.
func SaveMixtapeSchedule(ctx context.Context, client s3.Client, cfg *internal.Config, schedule *DailyMixtapeSchedule) error {
	return client.WriteJSON(ctx, cfg.MixtapeScheduleJSONKey, schedule)
}

// LoadMixtapeSchedule loads the mixtape schedule from S3. Returns nil if not found.
func LoadMixtapeSchedule(ctx context.Context, client s3.Client, cfg *internal.Config) (*DailyMixtapeSchedule, error) {
	var schedule DailyMixtapeSchedule
	found, err := client.ReadJSON(ctx, cfg.MixtapeScheduleJSONKey, &schedule)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &schedule, nil
}

// GetOrCreateMixtapeSchedule returns today's mixtape schedule, creating it if needed.
func GetOrCreateMixtapeSchedule(ctx context.Context, client s3.Client, cfg *internal.Config, now time.Time) (*DailyMixtapeSchedule, error) {
	schedule, err := LoadMixtapeSchedule(ctx, client, cfg)
	if err == nil && schedule != nil && schedule.Date == now.Format("2006-01-02") && len(schedule.Entries) == defaultMixtapesPerDay {
		return schedule, nil
	}

	times := BuildDailyMixtapeSchedule(now, defaultMixtapesPerDay)
	entries := make([]MixtapeScheduleEntry, len(times))
	for i, t := range times {
		entries[i] = MixtapeScheduleEntry{Time: t}
	}

	schedule = &DailyMixtapeSchedule{
		Date:      now.Format("2006-01-02"),
		Entries:   entries,
		UpdatedAt: now,
	}

	_ = SaveMixtapeSchedule(ctx, client, cfg, schedule)

	return schedule, nil
}
