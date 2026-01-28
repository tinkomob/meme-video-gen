package video

import (
	"math/rand"
	"sort"
	"time"

	"meme-video-gen/internal/model"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func randomIndex(n int) int {
	if n <= 0 {
		return 0
	}
	return rand.Intn(n)
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
