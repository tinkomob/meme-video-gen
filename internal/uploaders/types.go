package uploaders

import "context"

// UploadResult represents the result of an upload operation
type UploadResult struct {
	Success  bool              `json:"success"`
	Platform string            `json:"platform"`
	URL      string            `json:"url,omitempty"`
	Error    string            `json:"error,omitempty"`
	Details  map[string]string `json:"details,omitempty"`
}

// UploadRequest represents a request to upload a video
type UploadRequest struct {
	VideoPath     string
	ThumbnailPath string
	Title         string
	Description   string
	Caption       string
	Tags          []string
	Privacy       string // public, unlisted, private
}

// Uploader is an interface for uploading videos to social media platforms
type Uploader interface {
	Upload(ctx context.Context, req *UploadRequest) (*UploadResult, error)
	Platform() string
}
