package scheduler

import (
	"context"
	"math/rand"
	"time"

	"meme-video-gen/internal"
	"meme-video-gen/internal/s3"
)

// ScheduleEntry represents a single scheduled post time
type ScheduleEntry struct {
	Time time.Time `json:"time"`
}

// DailySchedule holds the schedule for a single day
type DailySchedule struct {
	Date      string          `json:"date"` // YYYY-MM-DD
	Entries   []ScheduleEntry `json:"entries"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// BuildDailySchedule creates N evenly distributed times within the window [10:00, 24:00)
// with random jitter to avoid clustering
func BuildDailySchedule(date time.Time, count int) []time.Time {
	loc := time.FixedZone("Asia/Tomsk", 7*3600) // UTC+7

	// Window: 10:00 to 23:59:59
	start := time.Date(date.Year(), date.Month(), date.Day(), 10, 0, 0, 0, loc)
	end := time.Date(date.Year(), date.Month(), date.Day(), 23, 59, 59, 0, loc)

	totalSeconds := int(end.Sub(start).Seconds())
	if count <= 0 {
		return nil
	}
	if count == 1 {
		// Single slot at midpoint
		return []time.Time{start.Add(time.Duration(totalSeconds/2) * time.Second)}
	}

	segmentSeconds := float64(totalSeconds) / float64(count)
	jitterMax := int(segmentSeconds / 3)
	if jitterMax > 1800 {
		jitterMax = 1800 // Cap jitter to 30 minutes
	}

	var times []time.Time
	for i := 0; i < count; i++ {
		segStart := float64(i) * segmentSeconds
		segEnd := float64(i+1) * segmentSeconds
		center := (segStart + segEnd) / 2

		// Apply random jitter
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

// SaveSchedule saves the schedule to S3
func SaveSchedule(ctx context.Context, client s3.Client, cfg *internal.Config, schedule *DailySchedule) error {
	return client.WriteJSON(ctx, "schedule.json", schedule)
}

// LoadSchedule loads the schedule from S3
func LoadSchedule(ctx context.Context, client s3.Client) (*DailySchedule, error) {
	var schedule DailySchedule
	found, err := client.ReadJSON(ctx, "schedule.json", &schedule)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &schedule, nil
}

// GetOrCreateSchedule returns today's schedule, creating it if needed
func GetOrCreateSchedule(ctx context.Context, client s3.Client, cfg *internal.Config, now time.Time) (*DailySchedule, error) {

	schedule, err := LoadSchedule(ctx, client)
	if err == nil && schedule != nil && schedule.Date == now.Format("2006-01-02") && len(schedule.Entries) == cfg.DailyGenerations {
		return schedule, nil
	}

	// Create new schedule for today
	times := BuildDailySchedule(now, cfg.DailyGenerations)
	entries := make([]ScheduleEntry, len(times))
	for i, t := range times {
		entries[i] = ScheduleEntry{Time: t}
	}

	schedule = &DailySchedule{
		Date:      now.Format("2006-01-02"),
		Entries:   entries,
		UpdatedAt: now,
	}

	// Try to save but don't fail if we can't (might be permission issue)
	_ = SaveSchedule(ctx, client, cfg, schedule)

	return schedule, nil
}

// GetNextScheduledTime returns the next scheduled time after 'now'
func GetNextScheduledTime(schedule *DailySchedule, now time.Time) *time.Time {
	for _, entry := range schedule.Entries {
		if entry.Time.After(now) {
			return &entry.Time
		}
	}
	return nil
}
