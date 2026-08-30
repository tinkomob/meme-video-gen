package friends

import (
	"context"
	"testing"
)

func TestPartNumber(t *testing.T) {
	tests := []struct {
		title string
		want  int
	}{
		{title: "Эпизод. Часть 1", want: 1},
		{title: "Эпизод, часть первая", want: 1},
		{title: "The Episode — Part II", want: 2},
		{title: "Эпизод. Часть третья", want: 3},
		{title: "Обычный эпизод", want: 0},
	}
	for _, test := range tests {
		t.Run(test.title, func(t *testing.T) {
			if got := partNumber(Episode{TitleRU: test.title}); got != test.want {
				t.Fatalf("partNumber() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestRandomStartsMultiPartStoryAtFirstPart(t *testing.T) {
	episodes := []Episode{
		{SeasonNumber: 1, EpisodeNumberInSeason: 10, TitleRU: "История. Часть 1"},
		{SeasonNumber: 1, EpisodeNumberInSeason: 11, TitleRU: "История. Часть 2"},
		{SeasonNumber: 1, EpisodeNumberInSeason: 12, TitleRU: "История. Часть 3"},
	}
	service := &Service{loaded: true, episodes: episodes}
	got, err := service.Random(context.Background(), nil)
	if err != nil || got.ID() != episodes[0].ID() {
		t.Fatalf("Random() = (%#v, %v), want %s", got, err, episodes[0].ID())
	}
}

func TestMatchesEpisodeFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		season   int
		episode  int
		want     bool
	}{
		{name: "ordinary episode", filename: "FriendsS01E01.BDRip.mp4", season: 1, episode: 1, want: true},
		{name: "season label before episode code", filename: "Season 2_FriendsS02E01.BDRip.mp4", season: 2, episode: 1, want: true},
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
