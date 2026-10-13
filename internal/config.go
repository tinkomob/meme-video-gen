package internal

import (
	"errors"
	"os"
	"strconv"
)

type Config struct {
	TelegramToken string
	S3Endpoint    string
	S3Region      string
	S3Bucket      string
	S3AccessKey   string
	S3SecretKey   string

	SongsJSONKey            string
	EngagementConfigJSONKey string

	SongsPrefix   string
	TokensPrefix  string
	PayloadPrefix string

	PostsChatID int64
	OwnerChatID int64 // personal chat ID for bot owner notifications
	Silent      bool
}

func LoadConfig() (Config, error) {
	cfg := Config{
		TelegramToken:           os.Getenv("TELEGRAM_BOT_TOKEN"),
		S3Endpoint:              os.Getenv("S3_ENDPOINT"),
		S3Region:                os.Getenv("S3_REGION"),
		S3Bucket:                os.Getenv("S3_BUCKET"),
		S3AccessKey:             firstNonEmpty(os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_ACCESS_KEY_ID")),
		S3SecretKey:             firstNonEmpty(os.Getenv("S3_SECRET_ACCESS_KEY"), os.Getenv("S3_SECRET_ACCESS_KEY_ID")),
		SongsJSONKey:            "songs.json",
		EngagementConfigJSONKey: "engagement_config.json",

		SongsPrefix:   "songs/",
		TokensPrefix:  "tokens/",
		PayloadPrefix: "payload/",

		PostsChatID: 0,
		Silent:      true,
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
