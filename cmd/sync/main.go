package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"meme-video-gen/internal"
	"meme-video-gen/internal/audio"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/s3"
	"meme-video-gen/internal/sources"
	"meme-video-gen/internal/video"

	"github.com/joho/godotenv"
)

func main() {
	// Load .env file if it exists
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../.env")

	var (
		syncSources = flag.Bool("sync-sources", false, "Synchronize sources.json with S3 sources/ folder")
		syncMemes   = flag.Bool("sync-memes", false, "Synchronize memes.json with S3 memes/ folder")
		syncAll     = flag.Bool("sync-all", false, "Synchronize both sources and memes")
	)
	flag.Parse()

	if !*syncSources && !*syncMemes && !*syncAll {
		fmt.Println("Usage: sync [-sync-sources] [-sync-memes] [-sync-all]")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  -sync-sources    Synchronize sources.json with S3 sources/ folder")
		fmt.Println("  -sync-memes      Synchronize memes.json with S3 memes/ folder")
		fmt.Println("  -sync-all        Synchronize both sources and memes")
		os.Exit(1)
	}

	cfg, err := internal.LoadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	log, err := logging.New("sync.log")
	if err != nil {
		fmt.Printf("Error creating logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Close()

	s3Client, err := s3.New(cfg)
	if err != nil {
		log.Errorf("Error creating S3 client: %v", err)
		os.Exit(1)
	}

	ctx := context.Background()

	if *syncAll || *syncSources {
		fmt.Println("=== Synchronizing sources.json with S3 sources/ folder ===")
		scraper := sources.NewScraper(cfg, s3Client, log)
		if err := scraper.SyncWithS3(ctx); err != nil {
			log.Errorf("Error syncing sources: %v", err)
			fmt.Printf("❌ Error syncing sources: %v\n", err)
		} else {
			fmt.Println("✅ Sources synchronized successfully")
		}
	}

	if *syncAll || *syncMemes {
		fmt.Println("=== Synchronizing memes.json with S3 memes/ folder ===")
		audioIdx := audio.NewIndexer(cfg, s3Client, log)
		scraper := sources.NewScraper(cfg, s3Client, log)
		generator := video.NewGenerator(cfg, s3Client, log, audioIdx, scraper)
		if err := generator.SyncWithS3(ctx); err != nil {
			log.Errorf("Error syncing memes: %v", err)
			fmt.Printf("❌ Error syncing memes: %v\n", err)
		} else {
			fmt.Println("✅ Memes synchronized successfully")
		}
	}

	fmt.Println("=== Synchronization complete ===")
}
