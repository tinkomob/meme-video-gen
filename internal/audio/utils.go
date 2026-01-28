package audio

import (
	"math/rand"
	"time"
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
