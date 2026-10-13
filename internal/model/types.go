package model

import "time"

type Song struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Author     string    `json:"author"`
	SourceURL  string    `json:"source_url"`
	AudioKey   string    `json:"audio_key"` // s3 key
	DurationS  float64   `json:"duration_s"`
	AddedAt    time.Time `json:"added_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	SHA256     string    `json:"sha256"`
}

type SongsIndex struct {
	UpdatedAt time.Time `json:"updated_at"`
	Items     []Song    `json:"items"`
}
