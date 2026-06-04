package sources

import (
	"context"
	"sync"
	"time"
)

const apiCallCountsKey = "api_call_counts.json"

type apiCallCounts struct {
	Date           string `json:"date"`
	HumorAPICalls  int    `json:"humor_api_calls"`
	APILeagueCalls int    `json:"apileague_calls"`
}

// memeAPIRateLimiter enforces max 5 requests per minute for meme-api.com.
var memeAPIRateLimiter = &rateLimiter{
	maxPerMinute: 5,
}

type rateLimiter struct {
	mu           sync.Mutex
	windowStart  time.Time
	callsInWindow int
	maxPerMinute int
}

// allow returns true if a request is permitted within the rate limit.
func (r *rateLimiter) allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if now.Sub(r.windowStart) >= time.Minute {
		r.windowStart = now
		r.callsInWindow = 0
	}
	if r.callsInWindow >= r.maxPerMinute {
		return false
	}
	r.callsInWindow++
	return true
}

func today() string {
	return time.Now().UTC().Format("2006-01-02")
}

func (sc *Scraper) loadCallCounts(ctx context.Context) (apiCallCounts, error) {
	var counts apiCallCounts
	found, err := sc.s3.ReadJSON(ctx, apiCallCountsKey, &counts)
	if err != nil || !found || counts.Date != today() {
		return apiCallCounts{Date: today()}, nil
	}
	return counts, nil
}

func (sc *Scraper) saveCallCounts(ctx context.Context, counts apiCallCounts) error {
	return sc.s3.WriteJSON(ctx, apiCallCountsKey, &counts)
}

// checkAndIncrementHumorAPI returns true if the call is allowed (under limit), and increments the counter.
func (sc *Scraper) checkAndIncrementHumorAPI(ctx context.Context, limit int) bool {
	counts, err := sc.loadCallCounts(ctx)
	if err != nil {
		return false
	}
	if counts.HumorAPICalls >= limit {
		return false
	}
	counts.HumorAPICalls++
	_ = sc.saveCallCounts(ctx, counts)
	return true
}

// checkAndIncrementAPILeague returns true if the call is allowed (under limit), and increments the counter.
func (sc *Scraper) checkAndIncrementAPILeague(ctx context.Context, limit int) bool {
	counts, err := sc.loadCallCounts(ctx)
	if err != nil {
		return false
	}
	if counts.APILeagueCalls >= limit {
		return false
	}
	counts.APILeagueCalls++
	_ = sc.saveCallCounts(ctx, counts)
	return true
}
