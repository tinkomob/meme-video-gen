package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
	"meme-video-gen/internal"
	"meme-video-gen/internal/audio"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/s3"
	"meme-video-gen/internal/snippet"
)

func main() {
	output := flag.String("output", "song-snippet.mp4", "destination MP4 (must not exist)")
	style := flag.String("style", "random", "visual style: random, silver, violet, amber, aurora, prism, galaxy")
	variant := flag.Int("variant", -1, "geometry/palette variant: 0, 1, 2; -1 chooses randomly")
	songID := flag.String("song", "", "YouTube song ID from the S3 index (random if empty)")
	flag.Parse()
	if err := run(*output, *style, *songID, *variant); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(output, style, songID string, variant int) error {
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("output already exists: %s", output)
	} else if !os.IsNotExist(err) {
		return err
	}
	_ = godotenv.Load(".env")
	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}
	logFile, err := os.CreateTemp("", "song-snippet-*.log")
	if err != nil {
		return err
	}
	logFile.Close()
	defer os.Remove(logFile.Name())
	log, err := logging.New(logFile.Name())
	if err != nil {
		return err
	}
	defer log.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	client, err := s3.New(cfg)
	if err != nil {
		return err
	}
	gen := snippet.NewGenerator(cfg, client, audio.NewIndexer(cfg, client, log), log)
	song, err := gen.GenerateSongSnippet(ctx, output, style, songID, variant)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	fmt.Printf("Created %s\nSong: %s — %s\nS3 audio: %s\n", abs, song.Author, song.Title, song.AudioKey)
	return nil
}
