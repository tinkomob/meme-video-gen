package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"meme-video-gen/internal/s3"
)

type snippetMemoryS3 struct {
	s3.Client
	data              []byte
	readErr, writeErr error
	writes            int
}

func (m *snippetMemoryS3) ReadJSON(_ context.Context, key string, out any) (bool, error) {
	if key != snippetScheduleKey {
		return false, fmt.Errorf("unexpected key %s", key)
	}
	if m.readErr != nil {
		return false, m.readErr
	}
	if len(m.data) == 0 {
		return false, nil
	}
	return true, json.Unmarshal(m.data, out)
}
func (m *snippetMemoryS3) WriteJSON(_ context.Context, key string, value any) error {
	if key != snippetScheduleKey {
		return fmt.Errorf("unexpected key %s", key)
	}
	if m.writeErr != nil {
		return m.writeErr
	}
	data, err := json.Marshal(value)
	if err == nil {
		m.data = data
		m.writes++
	}
	return err
}

func TestSnippetPlanGeneration(t *testing.T) {
	now := time.Date(2026, 10, 25, 0, 0, 0, 0, snippetLocation)
	r := rand.New(rand.NewSource(1))
	var previous *snippetDayPlan
	patterns := map[string]bool{}
	for day := 0; day < 3650; day++ {
		plan, err := newSnippetDay(now, previous, r)
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.validate(); err != nil {
			t.Fatalf("day %d: %v", day, err)
		}
		if previous != nil {
			gap := plan.Slots[0].Sub(previous.Slots[2])
			if gap < 6*time.Hour || gap > 10*time.Hour {
				t.Fatalf("cross-day gap: %s", gap)
			}
		}
		for _, handled := range plan.Handled {
			if handled {
				t.Fatal("future slots marked handled")
			}
		}
		patterns[plan.Slots[0].Format("15:04")+plan.Slots[1].Format("15:04")+plan.Slots[2].Format("15:04")] = true
		previous = &plan
		now = now.AddDate(0, 0, 1)
	}
	if len(patterns) < 3500 {
		t.Fatalf("not enough daily variation: %d patterns", len(patterns))
	}
}

func savedSnippetPlan(t *testing.T, m *snippetMemoryS3) snippetDayPlan {
	t.Helper()
	var p snippetDayPlan
	if err := json.Unmarshal(m.data, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func sampleSnippetPlan() snippetDayPlan {
	day := time.Date(2026, 10, 25, 0, 0, 0, 0, snippetLocation)
	return snippetDayPlan{Date: "2026-10-25", Slots: [3]time.Time{day.Add(130 * time.Minute), day.Add(630 * time.Minute), day.Add(1095 * time.Minute)}}
}

func TestSnippetPlanSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	s := snippetTestService(t)
	m := s.s3c.(*snippetMemoryS3)
	now := time.Date(2026, 10, 25, 0, 0, 0, 0, snippetLocation)
	if err := s.ensureSnippetPlan(ctx, now); err != nil {
		t.Fatal(err)
	}
	original := *s.snippetPlan
	restarted := snippetTestService(t)
	restarted.s3c = m
	if err := restarted.ensureSnippetPlan(ctx, now.Add(15*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if original.Date != restarted.snippetPlan.Date || original.Handled != restarted.snippetPlan.Handled || m.writes != 1 {
		t.Fatal("restart changed the saved daily plan")
	}
	for i, slot := range original.Slots {
		if !slot.Equal(restarted.snippetPlan.Slots[i]) {
			t.Fatal("restart changed slot time")
		}
	}
	if err := restarted.ensureSnippetPlan(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if restarted.snippetPlan.Date != "2026-10-26" || m.writes != 2 {
		t.Fatal("midnight did not create a new daily plan")
	}
	gap := restarted.snippetPlan.Slots[0].Sub(original.Slots[2])
	if gap < 6*time.Hour || gap > 10*time.Hour {
		t.Fatalf("bad rollover gap: %s", gap)
	}
}

func TestSnippetSlotClaimAndRestart(t *testing.T) {
	ctx := context.Background()
	s := snippetTestService(t)
	m := s.s3c.(*snippetMemoryS3)
	p := sampleSnippetPlan()
	if err := m.WriteJSON(ctx, snippetScheduleKey, p); err != nil {
		t.Fatal(err)
	}
	now := p.Slots[1].Add(10 * time.Second)
	due, err := s.claimSnippetSlot(ctx, now)
	if err != nil || !due {
		t.Fatalf("slot not claimed: due=%v err=%v", due, err)
	}
	stored := savedSnippetPlan(t, m)
	if stored.Handled != [3]bool{true, true, false} {
		t.Fatalf("incorrect claims: %v", stored.Handled)
	}
	restarted := snippetTestService(t)
	restarted.s3c = m
	due, err = restarted.claimSnippetSlot(ctx, now.Add(10*time.Second))
	if err != nil || due {
		t.Fatalf("restart repeated the same slot: due=%v err=%v", due, err)
	}
	for i, slot := range p.Slots {
		if !slot.Equal(savedSnippetPlan(t, m).Slots[i]) {
			t.Fatal("claim changed schedule times")
		}
	}
}

func TestSnippetScheduleStorageFailures(t *testing.T) {
	ctx := context.Background()
	s := snippetTestService(t)
	m := s.s3c.(*snippetMemoryS3)
	p := sampleSnippetPlan()
	if err := m.WriteJSON(ctx, snippetScheduleKey, p); err != nil {
		t.Fatal(err)
	}
	now := p.Slots[0].Add(10 * time.Second)
	m.readErr = errors.New("storage unavailable")
	if due, err := s.claimSnippetSlot(ctx, now); err == nil || due {
		t.Fatal("published without reading persisted state")
	}
	m.readErr = nil
	m.writeErr = errors.New("write failed")
	if due, err := s.claimSnippetSlot(ctx, now); err == nil || due {
		t.Fatal("published without saving claim")
	}
	if s.snippetPlan.Handled[0] || savedSnippetPlan(t, m).Handled[0] {
		t.Fatal("failed claim was consumed")
	}
	m.writeErr = nil
	if due, err := s.claimSnippetSlot(ctx, now); err != nil || !due {
		t.Fatalf("could not claim after recovery: %v", err)
	}
}

func TestSnippetScheduleSkipsMissedSlots(t *testing.T) {
	s := snippetTestService(t)
	m := s.s3c.(*snippetMemoryS3)
	p := sampleSnippetPlan()
	if err := m.WriteJSON(context.Background(), snippetScheduleKey, p); err != nil {
		t.Fatal(err)
	}
	if due, err := s.claimSnippetSlot(context.Background(), p.Slots[2].Add(2*time.Minute)); err != nil || due {
		t.Fatalf("missed slots replayed: %v", err)
	}
	if savedSnippetPlan(t, m).Handled != [3]bool{true, true, true} {
		t.Fatal("missed slots not marked handled")
	}
}

func TestSnippetScheduleRejectsBadState(t *testing.T) {
	for _, bad := range []string{`{"date":"2026-10-25"}`, `{"date":"bad"}`} {
		s := snippetTestService(t)
		m := s.s3c.(*snippetMemoryS3)
		m.data = []byte(bad)
		if err := s.ensureSnippetPlan(context.Background(), sampleSnippetPlan().Slots[0]); err == nil {
			t.Fatal("accepted invalid schedule")
		}
		if m.writes != 0 {
			t.Fatal("silently replaced invalid state")
		}
	}
}
