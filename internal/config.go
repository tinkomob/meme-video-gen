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

	GeminiAPIKey string
	SerpAPIKey   string

	SongsJSONKey    string
	SourcesJSONKey  string
	MemesJSONKey    string
	ScheduleJSONKey string

	SongsPrefix   string
	SourcesPrefix string
	MemesPrefix   string
	TokensPrefix  string
	PayloadPrefix string

	MaxSources int
	MaxMemes   int
	MaxAge     time.Duration

	DailyGenerations int   // количество отправок мемов в день
	PostsChatID      int64 // chat ID для отправки мемов по расписанию
}

func LoadConfig() (Config, error) {
	cfg := Config{
		TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		S3Endpoint:    os.Getenv("S3_ENDPOINT"),
		S3Region:      os.Getenv("S3_REGION"),
		S3Bucket:      os.Getenv("S3_BUCKET"),
		S3AccessKey:   firstNonEmpty(os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_ACCESS_KEY_ID")),
		S3SecretKey:   firstNonEmpty(os.Getenv("S3_SECRET_ACCESS_KEY"), os.Getenv("S3_SECRET_ACCESS_KEY_ID")),
		GeminiAPIKey:  firstNonEmpty(os.Getenv("GOOGLE_API_KEY"), os.Getenv("GEMINI_API_KEY")),
		SerpAPIKey:    os.Getenv("SERPAPI_KEY"),

		SongsJSONKey:    "songs.json",
		SourcesJSONKey:  "sources.json",
		MemesJSONKey:    "memes.json",
		ScheduleJSONKey: "schedule.json",

		SongsPrefix:   "songs/",
		SourcesPrefix: "sources/",
		MemesPrefix:   "memes/",
		TokensPrefix:  "tokens/",
		PayloadPrefix: "payload/",

		MaxSources:       50,
		MaxMemes:         10,
		MaxAge:           16 * time.Hour,
		DailyGenerations: 3,
		PostsChatID:      0,
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

	// Load MaxAge from env
	if v := os.Getenv("MAX_AGE"); v != "" {
		if duration, err := time.ParseDuration(v); err == nil {
			cfg.MaxAge = duration
		}
	}

	// Load DailyGenerations from env
	if v := os.Getenv("DAILY_GENERATIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.DailyGenerations = n
		}
	}

	// Load PostsChatID from env
	if v := os.Getenv("POSTS_CHAT_ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.PostsChatID = n
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
