package scheduler

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	snippetScheduleKey   = "snippet_schedule.json"
	snippetPostsPerDay   = 2
	snippetWindowMinutes = 24 * 60 / snippetPostsPerDay
	snippetMinGapMinutes = 10 * 60
	snippetMaxGapMinutes = 14 * 60
)

var snippetLocation = time.FixedZone("Asia/Tomsk", 7*3600)

type snippetDayPlan struct {
	Date    string      `json:"date"`
	Slots   []time.Time `json:"slots"`
	Handled []bool      `json:"handled"`
}

func (p snippetDayPlan) validate() error {
	day, err := time.ParseInLocation("2006-01-02", p.Date, snippetLocation)
	if err != nil {
		return fmt.Errorf("invalid snippet schedule date: %w", err)
	}
	window, minGap, maxGap := snippetWindowMinutes, snippetMinGapMinutes, snippetMaxGapMinutes
	// Read the previous three-slot format only to migrate saved schedules safely.
	if len(p.Slots) == 3 {
		window, minGap, maxGap = 480, 360, 600
	} else if len(p.Slots) != snippetPostsPerDay {
		return fmt.Errorf("invalid snippet slot count")
	}
	if len(p.Handled) != len(p.Slots) {
		return fmt.Errorf("invalid snippet handled state")
	}
	for i, slot := range p.Slots {
		if i > 0 && p.Handled[i] && !p.Handled[i-1] {
			return fmt.Errorf("invalid snippet handled order")
		}
		if slot.Before(day.Add(time.Duration(i*window)*time.Minute)) || !slot.Before(day.Add(time.Duration((i+1)*window)*time.Minute)) || slot.Second() != 0 || slot.Nanosecond() != 0 {
			return fmt.Errorf("invalid snippet slot %d", i)
		}
		if i > 0 {
			gap := slot.Sub(p.Slots[i-1])
			if gap < time.Duration(minGap)*time.Minute || gap > time.Duration(maxGap)*time.Minute {
				return fmt.Errorf("invalid snippet interval: %s", gap)
			}
		}
	}
	return nil
}

func newSnippetDay(now time.Time, previous *snippetDayPlan, r *rand.Rand) (snippetDayPlan, error) {
	now = now.In(snippetLocation)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, snippetLocation)
	p := snippetDayPlan{Date: day.Format("2006-01-02"), Slots: make([]time.Time, snippetPostsPerDay), Handled: make([]bool, snippetPostsPerDay)}
	var last time.Time
	if previous != nil && previous.Date == day.AddDate(0, 0, -1).Format("2006-01-02") {
		last = previous.Slots[len(previous.Slots)-1]
	}
	for i := range p.Slots {
		low, high := i*snippetWindowMinutes, (i+1)*snippetWindowMinutes-1
		if !last.IsZero() {
			lastMinute := int(last.Sub(day) / time.Minute)
			low, high = max(low, lastMinute+snippetMinGapMinutes), min(high, lastMinute+snippetMaxGapMinutes)
		}
		if low > high {
			return snippetDayPlan{}, fmt.Errorf("no valid snippet time for slot %d", i)
		}
		p.Slots[i] = day.Add(time.Duration(low+r.Intn(high-low+1)) * time.Minute)
		p.Handled[i] = p.Slots[i].Before(now)
		last = p.Slots[i]
	}
	return p, nil
}

func migrateSnippetDay(now time.Time, old snippetDayPlan, r *rand.Rand) (snippetDayPlan, error) {
	plan, err := newSnippetDay(now, nil, r)
	if err != nil {
		return snippetDayPlan{}, err
	}
	processed := 0
	for _, handled := range old.Handled {
		if handled {
			processed++
		}
	}
	if processed >= snippetPostsPerDay {
		for i := range plan.Handled {
			plan.Handled[i] = true
		}
	} else if processed == 1 {
		plan.Slots[0], plan.Handled[0] = old.Slots[0], true
		day, _ := time.ParseInLocation("2006-01-02", plan.Date, snippetLocation)
		firstMinute := int(plan.Slots[0].Sub(day) / time.Minute)
		low, high := max(snippetWindowMinutes, firstMinute+snippetMinGapMinutes), min(24*60-1, firstMinute+snippetMaxGapMinutes)
		plan.Slots[1] = day.Add(time.Duration(low+r.Intn(high-low+1)) * time.Minute)
		plan.Handled[1] = plan.Slots[1].Before(now)
	}
	return plan, nil
}

func (s *Service) ensureSnippetPlan(ctx context.Context, now time.Time) error {
	today := now.In(snippetLocation).Format("2006-01-02")
	if s.snippetPlan != nil && s.snippetPlan.Date == today && len(s.snippetPlan.Slots) == snippetPostsPerDay {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var saved snippetDayPlan
	found, err := s.s3c.ReadJSON(ctx, snippetScheduleKey, &saved)
	if err != nil {
		return fmt.Errorf("load snippet schedule: %w", err)
	}
	if found {
		if err := saved.validate(); err != nil {
			return err
		}
		if saved.Date > today {
			return fmt.Errorf("snippet schedule is dated in the future: %s", saved.Date)
		}
		if saved.Date == today && len(saved.Slots) == snippetPostsPerDay {
			s.snippetPlan = &saved
			return nil
		}
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	var plan snippetDayPlan
	if found && saved.Date == today {
		plan, err = migrateSnippetDay(now, saved, r)
	} else {
		plan, err = newSnippetDay(now, &saved, r)
	}
	if err != nil {
		return err
	}
	if err := s.s3c.WriteJSON(ctx, snippetScheduleKey, &plan); err != nil {
		return fmt.Errorf("save snippet schedule: %w", err)
	}
	s.snippetPlan = &plan
	s.log.Infof("snippet schedule %s Tomsk: %s, %s", plan.Date, plan.Slots[0].In(snippetLocation).Format("15:04"), plan.Slots[1].In(snippetLocation).Format("15:04"))
	return nil
}

// Persist claims before sending: a restart must not repeat a partially uploaded slot.
func (s *Service) claimSnippetSlot(ctx context.Context, now time.Time) (bool, error) {
	if err := s.ensureSnippetPlan(ctx, now); err != nil {
		return false, err
	}
	plan := *s.snippetPlan
	plan.Handled = append([]bool(nil), plan.Handled...)
	changed, due := false, false
	for i, slot := range plan.Slots {
		if plan.Handled[i] || slot.After(now) {
			continue
		}
		plan.Handled[i], changed = true, true
		if now.Sub(slot) < time.Minute {
			due = true
		}
	}
	if !changed {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.s3c.WriteJSON(ctx, snippetScheduleKey, &plan); err != nil {
		return false, fmt.Errorf("claim snippet slot: %w", err)
	}
	s.snippetPlan = &plan
	return due, nil
}

func (s *Service) registerSnippetSchedule(ctx context.Context) error {
	if err := s.ensureSnippetPlan(ctx, time.Now()); err != nil {
		s.log.Errorf("initialize snippet schedule: %v", err)
	}
	job := cron.FuncJob(func() {
		due, err := s.claimSnippetSlot(ctx, time.Now())
		if err == nil && due {
			err = s.publishSongSnippet(ctx)
		}
		if err != nil {
			s.log.Errorf("scheduled song snippet: %v", err)
		}
	})
	_, err := s.cron.AddJob("0 * * * * *", cron.NewChain(cron.SkipIfStillRunning(cron.DefaultLogger)).Then(job))
	return err
}
