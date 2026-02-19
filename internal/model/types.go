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

type SourceKind string

const (
	SourceKindPinterest SourceKind = "pinterest"
	SourceKindReddit    SourceKind = "reddit"
	SourceKindTwitter   SourceKind = "twitter"
	SourceKindUnknown   SourceKind = "unknown"
)

type SourceAsset struct {
	ID         string     `json:"id"`
	Kind       SourceKind `json:"kind"`
	SourceURL  string     `json:"source_url"`
	MediaKey   string     `json:"media_key"`
	MimeType   string     `json:"mime_type"`
	AddedAt    time.Time  `json:"added_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	Used       bool       `json:"used"`
	SHA256     string     `json:"sha256"`
	ImageHash  uint64     `json:"image_hash"` // Perceptual hash from imagehash2
}

type SourcesIndex struct {
	UpdatedAt time.Time     `json:"updated_at"`
	Items     []SourceAsset `json:"items"`
}

type Meme struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	VideoKey  string    `json:"video_key"`
	ThumbKey  string    `json:"thumb_key"`
	SongID    string    `json:"song_id"`
	SourceID  string    `json:"source_id"`
	CreatedAt time.Time `json:"created_at"`
	SHA256    string    `json:"sha256"`
	ImageHash uint64    `json:"image_hash"` // Perceptual hash of thumbnail
}

type MemesIndex struct {
	UpdatedAt time.Time `json:"updated_at"`
	Items     []Meme    `json:"items"`
}

// HashIndex stores perceptual hashes that have been seen before
// Used to prevent re-uploading/re-generating visually identical content
// even if the original item has been deleted from the active index
type ImageHashIndex struct {
	UpdatedAt time.Time `json:"updated_at"`
	Hashes    []uint64  `json:"hashes"` // Perceptual hashes of images
}

type VideoHashIndex struct {
	UpdatedAt time.Time `json:"updated_at"`
	Hashes    []uint64  `json:"hashes"` // Perceptual hashes of video thumbnails
}
