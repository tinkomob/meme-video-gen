package uploaders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

// YouTubeUploader handles YouTube video uploads
type YouTubeUploader struct {
	credentialsPath string
	tokenPath       string
}

// NewYouTubeUploader creates a new YouTube uploader
func NewYouTubeUploader(credentialsPath, tokenPath string) *YouTubeUploader {
	if credentialsPath == "" {
		credentialsPath = "client_secrets.json"
	}
	if tokenPath == "" {
		tokenPath = "token.pickle"
	}
	return &YouTubeUploader{
		credentialsPath: credentialsPath,
		tokenPath:       tokenPath,
	}
}

// NewYouTubeUploaderEenfinit creates a YouTube uploader for eenfinit account
func NewYouTubeUploaderEenfinit() *YouTubeUploader {
	return &YouTubeUploader{
		credentialsPath: os.Getenv("CLIENT_SECRETS_EENFINIT"),
		tokenPath:       os.Getenv("TOKEN_EENFINIT"),
	}
}

// Platform returns the platform name
func (y *YouTubeUploader) Platform() string {
	return "youtube"
}

// Upload uploads a video to YouTube
func (y *YouTubeUploader) Upload(ctx context.Context, req *UploadRequest) (*UploadResult, error) {
	service, err := y.authenticate(ctx)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "youtube",
			Error:    fmt.Sprintf("Authentication failed: %v", err),
		}, err
	}

	// Open video file
	videoFile, err := os.Open(req.VideoPath)
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "youtube",
			Error:    fmt.Sprintf("Failed to open video file: %v", err),
		}, err
	}
	defer videoFile.Close()

	// Set default values
	privacy := req.Privacy
	if privacy == "" {
		privacy = "public"
	}

	categoryId := "24" // Entertainment
	tags := req.Tags
	if len(tags) == 0 {
		tags = []string{"shorts", "meme", "funny"}
	}

	// Create video resource
	video := &youtube.Video{
		Snippet: &youtube.VideoSnippet{
			Title:       req.Title,
			Description: req.Description,
			Tags:        tags,
			CategoryId:  categoryId,
		},
		Status: &youtube.VideoStatus{
			PrivacyStatus:           privacy,
			SelfDeclaredMadeForKids: false,
		},
	}

	// Upload video
	call := service.Videos.Insert([]string{"snippet", "status"}, video)
	_, err = call.Media(videoFile).Do()
	if err != nil {
		return &UploadResult{
			Success:  false,
			Platform: "youtube",
			Error:    fmt.Sprintf("Upload failed: %v", err),
		}, err
	}

	return &UploadResult{
		Success:  true,
		Platform: "youtube",
		Details: map[string]string{
			"title":       req.Title,
			"description": req.Description,
		},
	}, nil
}

// authenticate authenticates with YouTube API
func (y *YouTubeUploader) authenticate(ctx context.Context) (*youtube.Service, error) {
	// Read credentials file
	credBytes, err := os.ReadFile(y.credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read credentials file: %v", err)
	}

	config, err := google.ConfigFromJSON(credBytes, youtube.YoutubeUploadScope, youtube.YoutubeScope)
	if err != nil {
		return nil, fmt.Errorf("unable to parse credentials file: %v", err)
	}

	// Load token from file if exists
	token, err := y.loadToken()
	if err != nil || token == nil || !token.Valid() {
		// Token doesn't exist or is invalid, need to get new one
		// For now, return error - in production you'd implement full OAuth flow
		return nil, fmt.Errorf("token not found or invalid. Please authenticate first")
	}

	// Create HTTP client with token
	client := config.Client(ctx, token)

	// Create YouTube service
	service, err := youtube.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("unable to create YouTube service: %v", err)
	}

	return service, nil
}

// loadToken loads OAuth token from file
func (y *YouTubeUploader) loadToken() (*oauth2.Token, error) {
	f, err := os.Open(y.tokenPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	token := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(token)
	return token, err
}

// saveToken saves OAuth token to file
func (y *YouTubeUploader) saveToken(token *oauth2.Token) error {
	f, err := os.Create(y.tokenPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return json.NewEncoder(f).Encode(token)
}
