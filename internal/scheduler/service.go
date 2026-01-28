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
	"meme-video-gen/internal/ai"
	"meme-video-gen/internal/audio"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/s3"
	"meme-video-gen/internal/sources"
	"meme-video-gen/internal/uploaders"
	"meme-video-gen/internal/video"
)

type MemeService interface {
	EnsureSongs(ctx context.Context) error
	EnsureSources(ctx context.Context) error
	EnsureMemes(ctx context.Context) error
	GenerateOneMeme(ctx context.Context) (*model.Meme, error)
	GetRandomMeme(ctx context.Context) (*model.Meme, error)
	GetRandomMemes(ctx context.Context, count int) ([]*model.Meme, error)
	DownloadMemeToTemp(ctx context.Context, meme *model.Meme) (string, error)
	DeleteMeme(ctx context.Context, memeID string) error
	ReplaceAudioInMeme(ctx context.Context, memeID string) (*model.Meme, error)
}

type Service struct {
	impl MemeService
	log  *logging.Logger
	cron *cron.Cron

	cfg internal.Config
	s3c s3.Client

	scheduleMux sync.Mutex
	schedule    *DailySchedule

	cfgMux           sync.Mutex
	monitor          *ResourceMonitor
	uploadersManager *uploaders.Manager
}

func (s *Service) Run(ctx context.Context) error {
	s.cron.Start()

	// Start resource monitor
	if s.monitor != nil {
		s.monitor.Start(ctx)
	}

	<-ctx.Done()

	// Stop resource monitor
	if s.monitor != nil {
		s.monitor.Stop()
	}

	ctxStop := s.cron.Stop()
	select {
	case <-ctxStop.Done():
		return nil
	case <-time.After(10 * time.Second):
		return errors.New("cron stop timeout")
	}
}

func (s *Service) Impl() MemeService { return s.impl }

func (s *Service) GetConfig() internal.Config {
	return s.cfg
}

func (s *Service) GetS3Client() s3.Client {
	return s.s3c
}

func (s *Service) GetSchedule() *DailySchedule {
	s.scheduleMux.Lock()
	defer s.scheduleMux.Unlock()
	return s.schedule
}

func (s *Service) GetMonitor() *ResourceMonitor {
	return s.monitor
}

func (s *Service) SetSchedule(sched *DailySchedule) {
	s.scheduleMux.Lock()
	defer s.scheduleMux.Unlock()
	s.schedule = sched
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
	if found && cfg.PostsChatID > 0 {
		s.cfg.PostsChatID = cfg.PostsChatID
		s.log.Infof("loaded POSTS_CHAT_ID=%d from S3", cfg.PostsChatID)
	}
	return nil
}

// GetSourcesCount returns the number of loaded sources
func (s *Service) GetSourcesCount(ctx context.Context) (int, error) {
	var sourcesIdx model.SourcesIndex
	found, err := s.s3c.ReadJSON(ctx, s.cfg.SourcesJSONKey, &sourcesIdx)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	return len(sourcesIdx.Items), nil
}

// GetMemesCount returns the number of generated meme videos
func (s *Service) GetMemesCount(ctx context.Context) (int, error) {
	var memesIdx model.MemesIndex
	found, err := s.s3c.ReadJSON(ctx, s.cfg.MemesJSONKey, &memesIdx)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	return len(memesIdx.Items), nil
}

// GetSongsCount returns the number of loaded audio songs
func (s *Service) GetSongsCount(ctx context.Context) (int, error) {
	var songsIdx model.SongsIndex
	found, err := s.s3c.ReadJSON(ctx, s.cfg.SongsJSONKey, &songsIdx)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	return len(songsIdx.Items), nil
}

// ClearSources removes all sources from the index and deletes source files from S3
func (s *Service) ClearSources(ctx context.Context) error {
	s.log.Infof("clearing all sources")

	// Read current sources index
	var sourcesIdx model.SourcesIndex
	found, err := s.s3c.ReadJSON(ctx, s.cfg.SourcesJSONKey, &sourcesIdx)
	if err != nil {
		return err
	}

	if !found || len(sourcesIdx.Items) == 0 {
		s.log.Infof("no sources to clear")
		return nil
	}

	// Delete all source files from S3
	for _, source := range sourcesIdx.Items {
		if err := s.s3c.Delete(ctx, source.MediaKey); err != nil {
			s.log.Errorf("failed to delete source %s: %v", source.ID, err)
		}
	}

	// Clear the sources index
	sourcesIdx.Items = []model.SourceAsset{}
	sourcesIdx.UpdatedAt = time.Now()

	if err := s.s3c.WriteJSON(ctx, s.cfg.SourcesJSONKey, &sourcesIdx); err != nil {
		return err
	}

	s.log.Infof("sources cleared successfully")
	return nil
}

// ClearMemes removes all memes from the index and deletes meme files from S3
func (s *Service) ClearMemes(ctx context.Context) error {
	s.log.Infof("clearing all memes")

	// Read current memes index
	var memesIdx model.MemesIndex
	found, err := s.s3c.ReadJSON(ctx, s.cfg.MemesJSONKey, &memesIdx)
	if err != nil {
		return err
	}

	if !found || len(memesIdx.Items) == 0 {
		s.log.Infof("no memes to clear")
		return nil
	}

	// Delete all meme files from S3
	for _, meme := range memesIdx.Items {
		if err := s.s3c.Delete(ctx, meme.VideoKey); err != nil {
			s.log.Errorf("failed to delete meme video %s: %v", meme.ID, err)
		}
		if err := s.s3c.Delete(ctx, meme.ThumbKey); err != nil {
			s.log.Errorf("failed to delete meme thumbnail %s: %v", meme.ID, err)
		}
	}

	// Clear the memes index
	memesIdx.Items = []model.Meme{}
	memesIdx.UpdatedAt = time.Now()

	if err := s.s3c.WriteJSON(ctx, s.cfg.MemesJSONKey, &memesIdx); err != nil {
		return err
	}

	s.log.Infof("memes cleared successfully")
	return nil
}

// SyncSources synchronizes sources.json with actual S3 sources/ folder
func (s *Service) SyncSources(ctx context.Context) error {
	s.log.Infof("syncing sources with S3")
	if impl, ok := s.impl.(*realImpl); ok {
		return impl.src.SyncWithS3(ctx)
	}
	return errors.New("sync not available for this implementation")
}

// SyncMemes synchronizes memes.json with actual S3 memes/ folder
func (s *Service) SyncMemes(ctx context.Context) error {
	s.log.Infof("syncing memes with S3")
	if impl, ok := s.impl.(*realImpl); ok {
		return impl.video.SyncWithS3(ctx)
	}
	return errors.New("sync not available for this implementation")
}

type realImpl struct {
	cfg   internal.Config
	s3    s3.Client
	log   *logging.Logger
	audio *audio.Indexer
	src   *sources.Scraper
	video *video.Generator
	ai    *ai.TitleGenerator
}

func (r *realImpl) EnsureSongs(ctx context.Context) error   { return r.audio.EnsureSongs(ctx) }
func (r *realImpl) EnsureSources(ctx context.Context) error { return r.src.EnsureSources(ctx) }
func (r *realImpl) EnsureMemes(ctx context.Context) error   { return r.video.EnsureMemes(ctx) }
func (r *realImpl) GenerateOneMeme(ctx context.Context) (*model.Meme, error) {
	return r.video.GenerateOneMeme(ctx)
}
func (r *realImpl) GetRandomMeme(ctx context.Context) (*model.Meme, error) {
	return r.video.GetRandomMeme(ctx)
}
func (r *realImpl) GetRandomMemes(ctx context.Context, count int) ([]*model.Meme, error) {
	return r.video.GetRandomMemes(ctx, count)
}
func (r *realImpl) DownloadMemeToTemp(ctx context.Context, meme *model.Meme) (string, error) {
	return r.video.DownloadMemeToTemp(ctx, meme)
}

func (r *realImpl) DeleteMeme(ctx context.Context, memeID string) error {
	r.log.Infof("service.DeleteMeme: START - memeID=%s", memeID)
	err := r.video.DeleteMeme(ctx, memeID)
	if err != nil {
		r.log.Errorf("service.DeleteMeme: FAILED - memeID=%s, err=%v", memeID, err)
		return err
	}
	r.log.Infof("service.DeleteMeme: SUCCESS - memeID=%s", memeID)
	return nil
}

func (r *realImpl) ReplaceAudioInMeme(ctx context.Context, memeID string) (*model.Meme, error) {
	r.log.Infof("service.ReplaceAudioInMeme: START - memeID=%s", memeID)
	meme, err := r.video.ReplaceAudioInMeme(ctx, memeID)
	if err != nil {
		r.log.Errorf("service.ReplaceAudioInMeme: FAILED - memeID=%s, err=%v", memeID, err)
		return nil, err
	}
	r.log.Infof("service.ReplaceAudioInMeme: SUCCESS - memeID=%s, new title=%s", memeID, meme.Title)
	return meme, nil
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
	srcScr := sources.NewScraper(cfg, s3c, log)
	vidGen := video.NewGenerator(cfg, s3c, log, audioIdx, srcScr)
	aiGen := ai.NewTitleGenerator(cfg.GeminiAPIKey, log)

	impl := &realImpl{cfg: cfg, s3: s3c, log: log, audio: audioIdx, src: srcScr, video: vidGen, ai: aiGen}

	c := cron.New(cron.WithSeconds())
	s := &Service{impl: impl, log: log, cron: c, cfg: cfg, s3c: s3c}

	// Hourly maintenance tasks (0 seconds, every hour)
	if _, err := c.AddFunc("0 0 * * * *", func() {
		log.Infof("cron: ensuring songs")
		if err := impl.EnsureSongs(context.Background()); err != nil {
			log.Errorf("cron ensure songs: %v", err)
		}
	}); err != nil {
		return nil, err
	}

	if _, err := c.AddFunc("0 0 * * * *", func() {
		log.Infof("cron: ensuring sources")
		if err := impl.EnsureSources(context.Background()); err != nil {
			log.Errorf("cron ensure sources: %v", err)
		}
	}); err != nil {
		return nil, err
	}

	if _, err := c.AddFunc("0 0 * * * *", func() {
		log.Infof("cron: ensuring memes")
		if err := impl.EnsureMemes(context.Background()); err != nil {
			log.Errorf("cron ensure memes: %v", err)
		}
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

	// Load or create today's schedule at startup
	go func() {
		time.Sleep(2 * time.Second)
		now := time.Now()
		sched, err := GetOrCreateSchedule(context.Background(), s3c, &cfg, now)
		if err != nil {
			log.Errorf("failed to load schedule: %v", err)
		} else {
			s.SetSchedule(sched)
			if sched != nil && len(sched.Entries) > 0 {
				log.Infof("loaded schedule for %s with %d entries", sched.Date, len(sched.Entries))
			}
		}
	}()

	// Create and configure resource monitor
	s.monitor = NewResourceMonitor(s, log)
	log.Infof("resource monitor initialized")

	// Initialize uploaders manager
	s.InitializeUploadersManager()

	return s, nil
}

// DeleteMemesOlderThan removes memes that were created more than duration ago
func (s *Service) DeleteMemesOlderThan(ctx context.Context, duration time.Duration) error {
	s.log.Infof("deleting memes older than %v", duration)

	// Load memes index
	var memesIdx model.MemesIndex
	found, err := s.s3c.ReadJSON(ctx, s.cfg.MemesJSONKey, &memesIdx)
	if err != nil {
		return err
	}
	if !found || len(memesIdx.Items) == 0 {
		s.log.Infof("no memes found to cleanup")
		return nil
	}

	now := time.Now()
	var itemsToKeep []model.Meme
	var deletedCount int

	for _, meme := range memesIdx.Items {
		age := now.Sub(meme.CreatedAt)
		if age > duration {
			// Delete meme files from S3
			s.log.Infof("deleting old meme: %s (age: %v)", meme.ID, age)

			if err := s.s3c.Delete(ctx, meme.VideoKey); err != nil {
				s.log.Errorf("failed to delete meme video %s: %v", meme.VideoKey, err)
			}

			if err := s.s3c.Delete(ctx, meme.ThumbKey); err != nil {
				s.log.Errorf("failed to delete meme thumb %s: %v", meme.ThumbKey, err)
			}

			deletedCount++
		} else {
			itemsToKeep = append(itemsToKeep, meme)
		}
	}

	if deletedCount == 0 {
		s.log.Infof("no old memes to delete")
		return nil
	}

	// Update memes index
	memesIdx.Items = itemsToKeep
	memesIdx.UpdatedAt = now

	if err := s.s3c.WriteJSON(ctx, s.cfg.MemesJSONKey, memesIdx); err != nil {
		s.log.Errorf("failed to update memes index: %v", err)
		return err
	}

	s.log.Infof("deleted %d old memes", deletedCount)
	return nil
}

// GetMemeByID retrieves a specific meme by ID
func (s *Service) GetMemeByID(ctx context.Context, memeID string) (*model.Meme, error) {
	s.log.Infof("GetMemeByID: searching for meme %s", memeID)

	// Load memes index
	var memesIdx model.MemesIndex
	found, err := s.s3c.ReadJSON(ctx, s.cfg.MemesJSONKey, &memesIdx)
	if err != nil {
		return nil, err
	}
	if !found || len(memesIdx.Items) == 0 {
		return nil, fmt.Errorf("no memes found in index")
	}

	// Find meme by ID
	for _, meme := range memesIdx.Items {
		if meme.ID == memeID {
			s.log.Infof("GetMemeByID: found meme %s", memeID)
			return &meme, nil
		}
	}

	return nil, fmt.Errorf("meme not found: %s", memeID)
}

// DownloadFileToTemp downloads a file from S3 to a temporary location
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

// InitializeUploadersManager initializes the uploaders manager
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
	s.log.Infof("InitializeYouTubeUploaderFromS3: token.json fields: %v", tokenObj)

	// Add YouTube uploader to manager
	ytUploader := uploaders.NewYouTubeUploader(clientSecretsPath, tokenPath)
	s.uploadersManager.AddUploader("youtube", ytUploader)

	s.log.Infof("InitializeYouTubeUploaderFromS3: YouTube uploader initialized successfully")
	return nil
}
