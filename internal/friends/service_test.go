package friends

import "testing"

func TestMatchesEpisodeFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		season   int
		episode  int
		want     bool
	}{
		{name: "ordinary episode", filename: "FriendsS01E01.BDRip.mp4", season: 1, episode: 1, want: true},
		{name: "combined first episode", filename: "FriendsS09E23-24.BDRip.RGzsRutracker.mp4", season: 9, episode: 23, want: true},
		{name: "combined second episode", filename: "FriendsS09E23-24.BDRip.RGzsRutracker.mp4", season: 9, episode: 24, want: true},
		{name: "outside combined range", filename: "FriendsS09E23-24.BDRip.RGzsRutracker.mp4", season: 9, episode: 22, want: false},
		{name: "wrong season", filename: "FriendsS09E23-24.BDRip.RGzsRutracker.mp4", season: 8, episode: 23, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := matchesEpisodeFilename(test.filename, test.season, test.episode); got != test.want {
				t.Fatalf("matchesEpisodeFilename() = %v, want %v", got, test.want)
			}
		})
	}
}
