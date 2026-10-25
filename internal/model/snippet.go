package model

import "time"

type SnippetDraft struct {
	ID        string            `json:"id"`
	ChatID    int64             `json:"chat_id"`
	UserID    int64             `json:"user_id"`
	Song      Song              `json:"song"`
	Style     string            `json:"style"`
	Variant   int               `json:"variant"`
	Selected  []string          `json:"selected"`
	Targets   map[string]string `json:"targets"`
	CreatedAt time.Time         `json:"created_at"`
}
