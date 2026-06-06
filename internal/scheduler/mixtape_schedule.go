package scheduler

import (
	"context"
	"math/rand"
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
	Enabled bool `json:"enabled"`
	Hour    int  `json:"hour"`
	Minute  int  `json:"minute"`
}

// MixtapeEngagementConfig holds engagement settings for Best Of and Teaser features.
type MixtapeEngagementConfig struct {
	BestOf BestOfEngagementConfig `json:"best_of"`
	Teaser TeaserEngagementConfig `json:"teaser"`
}

// DefaultEngagementConfig returns sensible defaults (both features enabled).
func DefaultEngagementConfig() *MixtapeEngagementConfig {
	return &MixtapeEngagementConfig{
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
// On first load (not found), defaults are saved to S3 so they persist.
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

// BuildDailyMixtapeSchedule creates count evenly distributed times across the full 24 h day
// with random jitter (±30 min, capped to 1/3 of segment).
func BuildDailyMixtapeSchedule(date time.Time, count int) []time.Time {
	loc := time.FixedZone("Asia/Tomsk", 7*3600)

	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	end := time.Date(date.Year(), date.Month(), date.Day(), 23, 59, 59, 0, loc)

	totalSeconds := int(end.Sub(start).Seconds())
	if count <= 0 {
		return nil
	}
	if count == 1 {
		return []time.Time{start.Add(time.Duration(totalSeconds/2) * time.Second)}
	}

	segmentSeconds := float64(totalSeconds) / float64(count)
	jitterMax := int(segmentSeconds / 3)
	if jitterMax > 1800 {
		jitterMax = 1800
	}

	var times []time.Time
	for i := 0; i < count; i++ {
		segStart := float64(i) * segmentSeconds
		segEnd := float64(i+1) * segmentSeconds
		center := (segStart + segEnd) / 2

		jitter := 0
		if jitterMax > 0 {
			jitter = rand.Intn(2*jitterMax+1) - jitterMax
		}

		t := start.Add(time.Duration(int(center)+jitter) * time.Second)
		if t.Before(start) {
			t = start
		}
		if t.After(end) {
			t = end
		}

		times = append(times, t)
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
