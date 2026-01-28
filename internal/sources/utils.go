package sources

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

func sortByAddedAt(items []model.SourceAsset, asc bool) []model.SourceAsset {
	sorted := make([]model.SourceAsset, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		if asc {
			return sorted[i].AddedAt.Before(sorted[j].AddedAt)
		}
		return sorted[i].AddedAt.After(sorted[j].AddedAt)
	})
	return sorted
}
