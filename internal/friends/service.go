package friends

import (
	"context"
	"fmt"
	"math/rand/v2"
	"path"
	"sort"
	"strings"
	"sync"

	"meme-video-gen/internal/s3"
)

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

// New accepts a season allow-list. A future all-seasons rollout only changes
// this argument; history and HTTP contracts stay the same.
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
	available := make([]Episode, 0, len(s.episodes))
	for _, episode := range s.episodes {
		if _, seen := excluded[episode.ID()]; !seen {
			available = append(available, episode)
		}
	}
	// Season 1 has fewer than 50 episodes. Once every eligible episode was
	// shown, start a new round; when all seasons are enabled the same history
	// naturally keeps the requested 50-episode no-repeat window.
	if len(available) == 0 {
		available = append(available, s.episodes...)
	}
	return available[rand.IntN(len(available))], nil
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

	files := make(map[string]s3.ObjectInfo, len(objects))
	for _, object := range objects {
		base := path.Base(object.Key)
		if !strings.EqualFold(path.Ext(base), ".mp4") {
			continue
		}
		files[strings.ToLower(base)] = object
	}
	available := make([]Episode, 0, len(episodes))
	for _, episode := range episodes {
		if _, enabled := s.allowedSeasons[episode.SeasonNumber]; !enabled {
			continue
		}
		prefix := strings.ToLower(fmt.Sprintf("FriendsS%02dE%02d.", episode.SeasonNumber, episode.EpisodeNumberInSeason))
		for name, object := range files {
			if strings.HasPrefix(name, prefix) {
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
