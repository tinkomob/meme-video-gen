package video

import (
	"context"

	"meme-video-gen/internal/logging"
)

// MuxVideoWithAudio overlays the provided audio track onto an existing video file.
func MuxVideoWithAudio(ctx context.Context, videoPath, audioPath, outputPath string, log *logging.Logger) error {
	return replaceAudioInVideo(ctx, videoPath, audioPath, outputPath, log)
}
