package friends

import (
	"context"
	"fmt"
	"math/rand/v2"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"meme-video-gen/internal/s3"
)

// S3 files can have a season label before the actual episode code, for example
// "Season 2_FriendsS02E01...". The code itself remains the source of truth.
var episodeFilenamePattern = regexp.MustCompile(`(?i)friendss(\d{2})e(\d{2})(?:-(\d{2}))?(?:\.|\[|$)`)

// partMarkerPattern covers the title suffixes used for multi-part episodes in
// both the Russian metadata and the original English titles.
var partMarkerPattern = regexp.MustCompile(`(?i)(?:часть|part)\s*(?:№\s*)?([\p{L}\p{N}]+)`)

const (
	metadataKey = "friends/friends_series_list.json"
	mediaPrefix = "friends/"
)

type Episode struct {
	SeasonNumber          int    `json:"season_number"`
	EpisodeNumberInSeason int    `json:"episode_number_in_season"`
	TitleEng              string `json:"title_eng"`
	TitleRU               string `json:"title_ru"`
	VideoKey              string `json:"-"`
	VideoSize             int64  `json:"-"`
}

func (e Episode) ID() string {
	return fmt.Sprintf("s%02de%02d", e.SeasonNumber, e.EpisodeNumberInSeason)
}

type Service struct {
	s3             s3.Client
	allowedSeasons map[int]struct{}
	mux            sync.RWMutex
	loaded         bool
	episodes       []Episode
}

// New accepts an optional season allow-list. An empty list enables every season.
func New(client s3.Client, allowedSeasons []int) *Service {
	seasons := make(map[int]struct{}, len(allowedSeasons))
	for _, season := range allowedSeasons {
		seasons[season] = struct{}{}
	}
	return &Service{s3: client, allowedSeasons: seasons}
}

func (s *Service) Random(ctx context.Context, excludedIDs []string) (Episode, error) {
	if err := s.load(ctx); err != nil {
		return Episode{}, err
	}
	excluded := make(map[string]struct{}, len(excludedIDs))
	for _, id := range excludedIDs {
		excluded[id] = struct{}{}
	}

	s.mux.RLock()
	defer s.mux.RUnlock()
	eligible := randomEligibleEpisodes(s.episodes)
	available := make([]Episode, 0, len(eligible))
	for _, episode := range eligible {
		if _, seen := excluded[episode.ID()]; !seen {
			available = append(available, episode)
		}
	}
	// If the catalogue contains fewer episodes than the recent-history window,
	// start a new round only after every eligible episode was shown.
	if len(available) == 0 {
		available = append(available, eligible...)
	}
	return available[rand.IntN(len(available))], nil
}

func randomEligibleEpisodes(episodes []Episode) []Episode {
	available := make([]Episode, 0, len(episodes))
	for _, episode := range episodes {
		// Later parts are never a random starting point. Their corresponding
		// first part remains eligible, so a story always starts at the beginning.
		if partNumber(episode) <= 1 {
			available = append(available, episode)
		}
	}
	return available
}

// IsFirstPart reports whether an episode begins a multi-part story.
func (e Episode) IsFirstPart() bool { return partNumber(e) == 1 }

func partNumber(episode Episode) int {
	for _, title := range []string{episode.TitleRU, episode.TitleEng} {
		match := partMarkerPattern.FindStringSubmatch(title)
		if len(match) == 0 {
			continue
		}
		if number := namedPartNumber(strings.ToLower(match[1])); number > 0 {
			return number
		}
	}
	return 0
}

func namedPartNumber(value string) int {
	if number, err := strconv.Atoi(value); err == nil {
		return number
	}
	switch value {
	case "первый", "первая", "первое", "первые", "first", "one", "i":
		return 1
	case "второй", "вторая", "второе", "вторые", "second", "two", "ii":
		return 2
	case "третий", "третья", "третье", "третьи", "third", "three", "iii":
		return 3
	case "четвертый", "четвёртый", "четвертая", "четвёртая", "fourth", "four", "iv":
		return 4
	}
	return 0
}

func (s *Service) EpisodeByID(ctx context.Context, id string) (Episode, bool, error) {
	if err := s.load(ctx); err != nil {
		return Episode{}, false, err
	}
	s.mux.RLock()
	defer s.mux.RUnlock()
	for _, episode := range s.episodes {
		if episode.ID() == id {
			return episode, true, nil
		}
	}
	return Episode{}, false, nil
}

func (s *Service) Open(ctx context.Context, episode Episode) (*s3.ObjectReader, error) {
	return s.s3.GetReader(ctx, episode.VideoKey)
}

func (s *Service) OpenRange(ctx context.Context, episode Episode, start, end int64) (*s3.ObjectReader, error) {
	return s.s3.GetRangeReader(ctx, episode.VideoKey, start, end)
}

func (s *Service) load(ctx context.Context) error {
	s.mux.RLock()
	loaded := s.loaded
	s.mux.RUnlock()
	if loaded {
		return nil
	}
	s.mux.Lock()
	defer s.mux.Unlock()
	if s.loaded {
		return nil
	}

	var episodes []Episode
	found, err := s.s3.ReadJSON(ctx, metadataKey, &episodes)
	if err != nil {
		return fmt.Errorf("read Friends episode list: %w", err)
	}
	if !found {
		return fmt.Errorf("Friends episode list %q was not found", metadataKey)
	}
	objects, err := s.s3.List(ctx, mediaPrefix)
	if err != nil {
		return fmt.Errorf("list Friends videos: %w", err)
	}

	files := make([]s3.ObjectInfo, 0, len(objects))
	for _, object := range objects {
		base := path.Base(object.Key)
		if !strings.EqualFold(path.Ext(base), ".mp4") {
			continue
		}
		files = append(files, object)
	}
	available := make([]Episode, 0, len(episodes))
	for _, episode := range episodes {
		if len(s.allowedSeasons) > 0 {
			if _, enabled := s.allowedSeasons[episode.SeasonNumber]; !enabled {
				continue
			}
		}
		for _, object := range files {
			if matchesEpisodeFilename(path.Base(object.Key), episode.SeasonNumber, episode.EpisodeNumberInSeason) {
				episode.VideoKey, episode.VideoSize = object.Key, object.Size
				available = append(available, episode)
				break
			}
		}
	}
	if len(available) == 0 {
		return fmt.Errorf("no Friends videos matched the selected seasons")
	}
	sort.Slice(available, func(i, j int) bool { return available[i].ID() < available[j].ID() })
	s.episodes, s.loaded = available, true
	return nil
}

// matchesEpisodeFilename recognizes ordinary files (S09E23) and combined
// episodes (S09E23-24), where the latter is valid for both episode numbers.
func matchesEpisodeFilename(filename string, season, episode int) bool {
	parts := episodeFilenamePattern.FindStringSubmatch(filename)
	if len(parts) == 0 {
		return false
	}
	fileSeason, _ := strconv.Atoi(parts[1])
	firstEpisode, _ := strconv.Atoi(parts[2])
	lastEpisode := firstEpisode
	if parts[3] != "" {
		lastEpisode, _ = strconv.Atoi(parts[3])
	}
	return fileSeason == season && episode >= firstEpisode && episode <= lastEpisode
}
