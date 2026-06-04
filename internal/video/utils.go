package video

import (
	"math/rand/v2"
	"sort"

	"meme-video-gen/internal/model"
)

func randomIndex(n int) int {
	if n <= 0 {
		return 0
	}
	return rand.IntN(n)
}

// randomAudioOffset returns a start time (in seconds) for sampling a clip from a song.
// It avoids the first and last ~10% of the track (clamped to [5s, 30s]) so the
// generated meme never starts right at the intro or fades out at the very end.
func randomAudioOffset(totalDuration, clipDuration float64) float64 {
	margin := totalDuration * 0.10
	if margin < 5.0 {
		margin = 5.0
	}
	if margin > 30.0 {
		margin = 30.0
	}
	minStart := margin
	maxStart := totalDuration - clipDuration - margin
	if maxStart <= minStart {
		return 0
	}
	return minStart + rand.Float64()*(maxStart-minStart)
}

func sortMemesByCreated(items []model.Meme, asc bool) []model.Meme {
	sorted := make([]model.Meme, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		if asc {
			return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
		}
		return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
	})
	return sorted
}
