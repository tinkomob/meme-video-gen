package video

import (
	"context"

	"meme-video-gen/internal/logging"
)

// MuxVideoWithAudio overlays the provided audio track onto an existing video file.
func MuxVideoWithAudio(ctx context.Context, videoPath, audioPath, outputPath string, log *logging.Logger) error {
	return replaceAudioInVideo(ctx, videoPath, audioPath, outputPath, log)
}

// MuxVerticalVideoWithAudio replaces the audio and renders a 1080x1920 video.
// It is used for user supplied music videos so they are uploaded as vertical Shorts.
func MuxVerticalVideoWithAudio(ctx context.Context, videoPath, audioPath, outputPath string, log *logging.Logger) error {
	return replaceAudioInVerticalVideo(ctx, videoPath, audioPath, outputPath, log)
}
