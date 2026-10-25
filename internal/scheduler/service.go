package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"meme-video-gen/internal"
	"meme-video-gen/internal/audio"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/s3"
	"meme-video-gen/internal/snippet"
	"meme-video-gen/internal/uploaders"
)

type SongService interface {
	EnsureSongs(ctx context.Context) error
	DownloadSongToTemp(ctx context.Context, song *model.Song) (string, error)
}

type Service struct {
	impl SongService
	log  *logging.Logger
	cron *cron.Cron

	cfg internal.Config
	s3c s3.Client

	cfgMux           sync.Mutex
	uploadersManager *uploaders.Manager
	snippetGen       *snippet.Generator
	snippetPlan      *snippetDayPlan

	cacheMux     sync.RWMutex
	cachedCounts map[string]cachedValue
	cacheTTL     time.Duration
}

type cachedValue struct {
	count     int
	timestamp time.Time
}

func (s *Service) Run(ctx context.Context) error {
	s.cron.Start()

	<-ctx.Done()

	ctxStop := s.cron.Stop()
	select {
	case <-ctxStop.Done():
		return nil
	case <-time.After(10 * time.Second):
		return errors.New("cron stop timeout")
	}
}

func (s *Service) Impl() SongService { return s.impl }

func (s *Service) GetConfig() internal.Config {
	s.cfgMux.Lock()
	defer s.cfgMux.Unlock()
	return s.cfg
}

func (s *Service) GetS3Client() s3.Client {
	return s.s3c
}

func (s *Service) SavePostsChatID(ctx context.Context, chatID int64) error {
	s.cfgMux.Lock()
	defer s.cfgMux.Unlock()
	s.cfg.PostsChatID = chatID
	type ConfigStore struct {
		PostsChatID int64 `json:"posts_chat_id"`
	}
	return s.s3c.WriteJSON(ctx, "config.json", &ConfigStore{PostsChatID: chatID})
}

func (s *Service) LoadPostsChatID(ctx context.Context) error {
	s.cfgMux.Lock()
	defer s.cfgMux.Unlock()
	type ConfigStore struct {
		PostsChatID int64 `json:"posts_chat_id"`
	}
	var cfg ConfigStore
	found, err := s.s3c.ReadJSON(ctx, "config.json", &cfg)
	if err != nil {
		return err
	}
	if found && cfg.PostsChatID != 0 {
		s.cfg.PostsChatID = cfg.PostsChatID
		s.log.Infof("loaded POSTS_CHAT_ID=%d from S3", cfg.PostsChatID)
	}
	return nil
}

// InvalidateCache clears the count cache to force re-read from S3 on next check
func (s *Service) InvalidateCache(cacheKey string) {
	s.cacheMux.Lock()
	defer s.cacheMux.Unlock()
	delete(s.cachedCounts, cacheKey)
	// s.log.Infof("cache invalidated: %s", cacheKey)
}

// InvalidateCacheAll clears all counts from cache
func (s *Service) InvalidateCacheAll() {
	s.cacheMux.Lock()
	defer s.cacheMux.Unlock()
	s.cachedCounts = make(map[string]cachedValue)
	// s.log.Infof("cache invalidated: all entries cleared")
}

func (s *Service) GetSongsCount(ctx context.Context) (int, error) {
	// Check cache first (60 second TTL)
	s.cacheMux.RLock()
	if cached, exists := s.cachedCounts["songs"]; exists && time.Since(cached.timestamp) < s.cacheTTL {
		s.cacheMux.RUnlock()
		// Cache hit - no S3 traffic
		return cached.count, nil
	}
	s.cacheMux.RUnlock()

	// Cache miss - read from S3
	s.log.Infof("GetSongsCount: cache miss, reading from S3")
	var songsIdx model.SongsIndex
	found, err := s.s3c.ReadJSON(ctx, s.cfg.SongsJSONKey, &songsIdx)
	if err != nil {
		return 0, err
	}

	count := 0
	if found {
		count = len(songsIdx.Items)
	}

	// Update cache
	s.cacheMux.Lock()
	s.cachedCounts["songs"] = cachedValue{count: count, timestamp: time.Now()}
	s.cacheMux.Unlock()

	return count, nil
}

type realImpl struct {
	audio *audio.Indexer
}

func (r *realImpl) EnsureSongs(ctx context.Context) error { return r.audio.EnsureSongs(ctx) }
func (r *realImpl) DownloadSongToTemp(ctx context.Context, song *model.Song) (string, error) {
	return r.audio.DownloadSongToTemp(ctx, song)
}

func BuildService(ctx context.Context, log *logging.Logger) (*Service, error) {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return nil, err
	}

	s3c, err := s3.New(cfg)
	if err != nil {
		return nil, err
	}

	audioIdx := audio.NewIndexer(cfg, s3c, log)
	impl := &realImpl{audio: audioIdx}
	snippetGen := snippet.NewGenerator(cfg, s3c, audioIdx, log)

	c := cron.New(cron.WithSeconds())
	s := &Service{
		impl:         impl,
		log:          log,
		cron:         c,
		cfg:          cfg,
		s3c:          s3c,
		cachedCounts: make(map[string]cachedValue),
		cacheTTL:     90 * time.Second,
		snippetGen:   snippetGen,
	}

	// Hourly maintenance tasks (0 seconds, every hour)
	if _, err := c.AddFunc("0 0 * * * *", func() {
		log.Infof("cron: ensuring songs")
		if err := impl.EnsureSongs(context.Background()); err != nil {
			log.Errorf("cron ensure songs: %v", err)
		}
		s.InvalidateCache("songs") // Invalidate cache after updating
	}); err != nil {
		return nil, err
	}

	// Load POSTS_CHAT_ID from S3 at startup
	go func() {
		time.Sleep(1 * time.Second)
		if err := s.LoadPostsChatID(context.Background()); err != nil {
			log.Errorf("failed to load POSTS_CHAT_ID: %v", err)
		}
	}()

	// Initialize uploaders manager
	s.InitializeUploadersManager()
	if err := s.registerSnippetSchedule(ctx); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Service) DownloadFileToTemp(ctx context.Context, s3Key string, prefix string) (string, error) {
	s.log.Infof("DownloadFileToTemp: downloading %s with prefix %s", s3Key, prefix)

	// Determine file extension from S3 key
	ext := ""
	if strings.Contains(s3Key, ".") {
		parts := strings.Split(s3Key, ".")
		ext = "." + parts[len(parts)-1]
	}

	// Create temp file with proper extension
	tempFile, err := os.CreateTemp(os.TempDir(), fmt.Sprintf("%s-*%s", prefix, ext))
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tempFile.Close()

	tempPath := tempFile.Name()

	// Download from S3
	reader, err := s.s3c.GetReader(ctx, s3Key)
	if err != nil {
		os.Remove(tempPath)
		return "", fmt.Errorf("failed to open S3 file: %w", err)
	}
	defer reader.Reader.Close()

	// Copy to temp file
	_, err = tempFile.ReadFrom(reader.Reader)
	if err != nil {
		os.Remove(tempPath)
		return "", fmt.Errorf("failed to download file: %w", err)
	}

	s.log.Infof("DownloadFileToTemp: successfully downloaded to %s", tempPath)
	return tempPath, nil
}

// GetUploadersManager returns the uploaders manager
func (s *Service) GetUploadersManager() *uploaders.Manager {
	return s.uploadersManager
}

// GetTitleGenerator returns the AI title generator (used for idea generation)
func (s *Service) GetAllSongs(ctx context.Context) ([]*model.Song, error) {
	var songsIdx model.SongsIndex
	found, err := s.s3c.ReadJSON(ctx, s.cfg.SongsJSONKey, &songsIdx)
	if err != nil {
		return nil, fmt.Errorf("read songs.json: %w", err)
	}
	if !found || len(songsIdx.Items) == 0 {
		return nil, fmt.Errorf("no songs found")
	}

	// Convert to pointers for consistency
	result := make([]*model.Song, len(songsIdx.Items))
	for i := range songsIdx.Items {
		result[i] = &songsIdx.Items[i]
	}
	return result, nil
}

// GetSongByID returns a song by ID
func (s *Service) GetSongByID(ctx context.Context, songID string) (*model.Song, error) {
	songs, err := s.GetAllSongs(ctx)
	if err != nil {
		return nil, err
	}

	for _, song := range songs {
		if song.ID == songID {
			return song, nil
		}
	}
	return nil, fmt.Errorf("song not found: %s", songID)
}

// GetRandomSong returns a random song from all available songs
func (s *Service) GetRandomSong(ctx context.Context) (*model.Song, error) {
	songs, err := s.GetAllSongs(ctx)
	if err != nil {
		return nil, err
	}

	if len(songs) == 0 {
		return nil, fmt.Errorf("no songs available")
	}

	// Use the same randomization as video generator
	idx := int(time.Now().UnixNano() % int64(len(songs)))
	return songs[idx], nil
}

// SearchSongs searches for songs by title or author
func (s *Service) SearchSongs(ctx context.Context, query string) ([]*model.Song, error) {
	songs, err := s.GetAllSongs(ctx)
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, fmt.Errorf("search query is empty")
	}

	var results []*model.Song
	for _, song := range songs {
		// Search in title and author
		if strings.Contains(strings.ToLower(song.Title), query) ||
			strings.Contains(strings.ToLower(song.Author), query) {
			results = append(results, song)
		}
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no songs found matching query: %s", query)
	}

	return results, nil
}

// SearchSongsForIdea searches for songs by title or author from eenfinit (excluding dee bill)
func (s *Service) InitializeUploadersManager() {
	s.uploadersManager = uploaders.NewManager()
	s.log.Infof("uploaders manager initialized with %d platforms", len(s.uploadersManager.AvailablePlatforms()))
}

// InitializeYouTubeUploaderFromS3 loads YouTube credentials from S3 and initializes YouTube uploader
func (s *Service) InitializeYouTubeUploaderFromS3(ctx context.Context) error {
	s.log.Infof("InitializeYouTubeUploaderFromS3: loading YouTube credentials from S3")

	// Try different possible paths for credentials in S3
	possibleClientSecrets := []string{
		"client_secrets.json",
		"tokens/client_secrets.json",
		"bot-uploads/client_secrets.json",
		s.cfg.TokensPrefix + "client_secrets.json",
	}

	possibleTokens := []string{
		"token.json",
		"tokens/token.json",
		"bot-uploads/token.json",
		s.cfg.TokensPrefix + "token.json",
	}

	var clientSecretsPath, tokenPath string

	// Try to find and download client_secrets.json
	for _, key := range possibleClientSecrets {
		s.log.Infof("InitializeYouTubeUploaderFromS3: trying to download %s from S3", key)
		path, err := s.DownloadFileToTemp(ctx, key, "client_secrets")
		if err == nil {
			clientSecretsPath = path
			s.log.Infof("InitializeYouTubeUploaderFromS3: found client_secrets.json at %s", key)
			break
		}
	}

	if clientSecretsPath == "" {
		s.log.Errorf("InitializeYouTubeUploaderFromS3: could not find client_secrets.json in any location")
		return fmt.Errorf("client_secrets.json not found in S3")
	}

	// Try to find and download token.json
	for _, key := range possibleTokens {
		s.log.Infof("InitializeYouTubeUploaderFromS3: trying to download %s from S3", key)
		path, err := s.DownloadFileToTemp(ctx, key, "token")
		if err == nil {
			tokenPath = path
			s.log.Infof("InitializeYouTubeUploaderFromS3: found token.json at %s", key)
			break
		}
	}

	if tokenPath == "" {
		os.Remove(clientSecretsPath)
		s.log.Errorf("InitializeYouTubeUploaderFromS3: could not find token.json in any location")
		return fmt.Errorf("token.json not found in S3")
	}

	// Verify files exist and are readable
	if _, err := os.Stat(clientSecretsPath); err != nil {
		s.log.Errorf("InitializeYouTubeUploaderFromS3: client_secrets.json not accessible: %v", err)
		return err
	}
	if _, err := os.Stat(tokenPath); err != nil {
		s.log.Errorf("InitializeYouTubeUploaderFromS3: token.json not accessible: %v", err)
		return err
	}

	// Try to read and validate token file
	tokenData, err := os.ReadFile(tokenPath)
	if err != nil {
		s.log.Errorf("InitializeYouTubeUploaderFromS3: failed to read token.json: %v", err)
		return err
	}

	var tokenObj map[string]interface{}
	if err := json.Unmarshal(tokenData, &tokenObj); err != nil {
		s.log.Errorf("InitializeYouTubeUploaderFromS3: token.json is not valid JSON: %v", err)
		return err
	}

	// Add YouTube uploader to manager
	ytUploader := uploaders.NewYouTubeUploader(clientSecretsPath, tokenPath)
	s.uploadersManager.AddUploader("youtube", ytUploader)

	s.log.Infof("InitializeYouTubeUploaderFromS3: YouTube uploader initialized successfully")
	return nil
}
