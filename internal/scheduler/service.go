package scheduler

import (
	"context"
	"errors"
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
	"meme-video-gen/internal/video"
)

type MemeService interface {
	EnsureSongs(ctx context.Context) error
	EnsureSources(ctx context.Context) error
	EnsureMemes(ctx context.Context) error
	GetRandomMeme(ctx context.Context) (*model.Meme, error)
	DownloadMemeToTemp(ctx context.Context, meme *model.Meme) (string, error)
}

type Service struct {
	impl MemeService
	log  *logging.Logger
	cron *cron.Cron

	cfg internal.Config
	s3c s3.Client

	scheduleMux sync.Mutex
	schedule    *DailySchedule

	cfgMux sync.Mutex
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
func (r *realImpl) GetRandomMeme(ctx context.Context) (*model.Meme, error) {
	return r.video.GetRandomMeme(ctx)
}
func (r *realImpl) DownloadMemeToTemp(ctx context.Context, meme *model.Meme) (string, error) {
	return r.video.DownloadMemeToTemp(ctx, meme)
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

	// Initial run - Start immediately without delay
	go func() {
		log.Infof("initial: ensuring songs")
		if err := impl.EnsureSongs(context.Background()); err != nil {
			log.Errorf("initial ensure songs failed: %v", err)
		}
		log.Infof("initial: ensuring sources")
		if err := impl.EnsureSources(context.Background()); err != nil {
			log.Errorf("initial ensure sources failed: %v", err)
		}
		log.Infof("initial: ensuring memes")
		if err := impl.EnsureMemes(context.Background()); err != nil {
			log.Errorf("initial ensure memes failed: %v", err)
		}
	}()

	return s, nil
}
