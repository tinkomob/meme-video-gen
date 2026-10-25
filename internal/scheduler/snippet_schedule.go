package scheduler

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/robfig/cron/v3"
)

const snippetScheduleKey = "snippet_schedule.json"

var snippetLocation = time.FixedZone("Asia/Tomsk", 7*3600)

type snippetDayPlan struct {
	Date    string       `json:"date"`
	Slots   [3]time.Time `json:"slots"`
	Handled [3]bool      `json:"handled"`
}

func (p snippetDayPlan) validate() error {
	day, err := time.ParseInLocation("2006-01-02", p.Date, snippetLocation)
	if err != nil {
		return fmt.Errorf("invalid snippet schedule date: %w", err)
	}
	for i, slot := range p.Slots {
		if slot.Before(day.Add(time.Duration(i)*8*time.Hour)) || !slot.Before(day.Add(time.Duration(i+1)*8*time.Hour)) || slot.Second() != 0 || slot.Nanosecond() != 0 {
			return fmt.Errorf("invalid snippet slot %d", i)
		}
		if i > 0 {
			gap := slot.Sub(p.Slots[i-1])
			if gap < 6*time.Hour || gap > 10*time.Hour {
				return fmt.Errorf("invalid snippet interval: %s", gap)
			}
		}
	}
	return nil
}

func newSnippetDay(now time.Time, previous *snippetDayPlan, r *rand.Rand) (snippetDayPlan, error) {
	now = now.In(snippetLocation)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, snippetLocation)
	p := snippetDayPlan{Date: day.Format("2006-01-02")}
	var last time.Time
	if previous != nil && previous.Date == day.AddDate(0, 0, -1).Format("2006-01-02") {
		last = previous.Slots[2]
	}
	for i := range p.Slots {
		low, high := i*480, (i+1)*480-1
		if !last.IsZero() {
			lastMinute := int(last.Sub(day) / time.Minute)
			low, high = max(low, lastMinute+360), min(high, lastMinute+600)
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

func (s *Service) ensureSnippetPlan(ctx context.Context, now time.Time) error {
	today := now.In(snippetLocation).Format("2006-01-02")
	if s.snippetPlan != nil && s.snippetPlan.Date == today {
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
		if saved.Date == today {
			s.snippetPlan = &saved
			return nil
		}
	}
	plan, err := newSnippetDay(now, &saved, rand.New(rand.NewSource(time.Now().UnixNano())))
	if err != nil {
		return err
	}
	if err := s.s3c.WriteJSON(ctx, snippetScheduleKey, &plan); err != nil {
		return fmt.Errorf("save snippet schedule: %w", err)
	}
	s.snippetPlan = &plan
	s.log.Infof("snippet schedule %s Tomsk: %s, %s, %s", plan.Date, plan.Slots[0].In(snippetLocation).Format("15:04"), plan.Slots[1].In(snippetLocation).Format("15:04"), plan.Slots[2].In(snippetLocation).Format("15:04"))
	return nil
}

// Persist claims before sending: a restart must not repeat a partially uploaded slot.
func (s *Service) claimSnippetSlot(ctx context.Context, now time.Time) (bool, error) {
	if err := s.ensureSnippetPlan(ctx, now); err != nil {
		return false, err
	}
	plan := *s.snippetPlan
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
