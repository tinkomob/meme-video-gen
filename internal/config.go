package internal

import (
	"errors"
	"os"
	"strconv"
	"time"
)

type Config struct {
	TelegramToken string
	S3Endpoint    string
	S3Region      string
	S3Bucket      string
	S3AccessKey   string
	S3SecretKey   string

	GeminiAPIKey     string
	OpenRouterAPIKey string
	SerpAPIKey       string
	BratuhaAPIKey    string
	HumorAPIKey  string
	APILeagueKey string

	SongsJSONKey           string
	SourcesJSONKey         string
	MemesJSONKey           string
	ScheduleJSONKey        string
	MixtapeScheduleJSONKey  string
	EngagementConfigJSONKey string

	ImageHashIndexKey      string // "image_hashes.json" - blacklist of image hashes
	VideoHashIndexKey      string // "video_hashes.json" - blacklist of video hashes
	DislikedSourcesJSONKey string // "disliked_sources.json" - temporarily blacklisted sources

	SongsPrefix   string
	SourcesPrefix string
	MemesPrefix   string
	TokensPrefix  string
	PayloadPrefix string

	MaxSources  int
	MaxMemes    int
	MaxMixtapes int
	MaxAge      time.Duration

	// Disliked sources grace period (default: 24 hours)
	// Sources blacklisted by user dislike won't be reused for this duration
	DislikedSourceGracePeriod time.Duration

	DailyGenerations  int   // количество отправок мемов в день
	PostsChatID       int64 // chat ID для отправки мемов по расписанию
	OwnerChatID       int64 // personal chat ID for bot owner notifications
	Silent            bool  // если true, не выводить информационные логи о загрузке источников
	DisableGeneration bool  // if true, skip source scraping and meme/mixtape generation cron tasks
}

func LoadConfig() (Config, error) {
	cfg := Config{
		TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		S3Endpoint:    os.Getenv("S3_ENDPOINT"),
		S3Region:      os.Getenv("S3_REGION"),
		S3Bucket:      os.Getenv("S3_BUCKET"),
		S3AccessKey:   firstNonEmpty(os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_ACCESS_KEY_ID")),
		S3SecretKey:   firstNonEmpty(os.Getenv("S3_SECRET_ACCESS_KEY"), os.Getenv("S3_SECRET_ACCESS_KEY_ID")),
		GeminiAPIKey:     firstNonEmpty(os.Getenv("GOOGLE_API_KEY"), os.Getenv("GEMINI_API_KEY")),
		OpenRouterAPIKey: os.Getenv("OPENROUTER_API_KEY"),
		SerpAPIKey:       os.Getenv("SERPAPI_KEY"),
		BratuhaAPIKey: os.Getenv("BRATUHA_API_KEY"),
		HumorAPIKey:  os.Getenv("HUMOR_API_KEY"),
		APILeagueKey: os.Getenv("APILEAGUE_API_KEY"),

		SongsJSONKey:           "songs.json",
		SourcesJSONKey:         "sources.json",
		MemesJSONKey:           "memes.json",
		ScheduleJSONKey:        "schedule.json",
		MixtapeScheduleJSONKey:  "mixtape_schedule.json",
		EngagementConfigJSONKey: "engagement_config.json",

		ImageHashIndexKey:      "image_hashes.json",
		VideoHashIndexKey:      "video_hashes.json",
		DislikedSourcesJSONKey: "disliked_sources.json",

		SongsPrefix:   "songs/",
		SourcesPrefix: "sources/",
		MemesPrefix:   "memes/",
		TokensPrefix:  "tokens/",
		PayloadPrefix: "payload/",

		MaxSources:                20,
		MaxMemes:                  10,
		MaxMixtapes:               5,
		MaxAge:                    24 * time.Hour,
		DislikedSourceGracePeriod: 24 * time.Hour, // 24 hours by default
		DailyGenerations:          5,
		PostsChatID:               0,
		Silent:                    true,
	}

	// Load MaxSources from env
	if v := os.Getenv("MAX_SOURCES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxSources = n
		}
	}

	// Load MaxMemes from env
	if v := os.Getenv("MAX_MEMES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxMemes = n
		}
	}

	// Load MaxMixtapes from env
	if v := os.Getenv("MAX_MIXTAPES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxMixtapes = n
		}
	}

	// Load MaxAge from env
	if v := os.Getenv("MAX_AGE"); v != "" {
		if duration, err := time.ParseDuration(v); err == nil {
			cfg.MaxAge = duration
		}
	}

	// Load DislikedSourceGracePeriod from env (e.g., "24h", "48h")
	if v := os.Getenv("DISLIKED_SOURCE_GRACE_PERIOD"); v != "" {
		if duration, err := time.ParseDuration(v); err == nil {
			cfg.DislikedSourceGracePeriod = duration
		}
	}

	// Load DailyGenerations from env
	if v := os.Getenv("DAILY_GENERATIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.DailyGenerations = n
		}
	}

	// Load DisableGeneration from env
	if v := os.Getenv("DISABLE_GENERATION"); v == "true" || v == "1" {
		cfg.DisableGeneration = true
	}

	// Load OwnerChatID from env
	if v := os.Getenv("OWNER_CHAT_ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n != 0 {
			cfg.OwnerChatID = n
		}
	}

	// Load PostsChatID from env
	if v := os.Getenv("POSTS_CHATID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.PostsChatID = n
		}

		// Load Silent from env (default: true)
		if v := os.Getenv("SILENT"); v != "" {
			cfg.Silent = v != "false" && v != "0" && v != ""
		}

	}
	if cfg.TelegramToken == "" {
		return cfg, errors.New("TELEGRAM_BOT_TOKEN is required")
	}
	if cfg.S3Endpoint == "" || cfg.S3Region == "" || cfg.S3Bucket == "" || cfg.S3AccessKey == "" || cfg.S3SecretKey == "" {
		return cfg, errors.New("S3_* env vars are required")
	}
	return cfg, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
