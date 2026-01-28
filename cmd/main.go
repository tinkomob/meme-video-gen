package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"meme-video-gen/internal/bot"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/scheduler"

	"github.com/joho/godotenv"
)

func main() {
	// Load .env file if it exists (try multiple paths)
	envPaths := []string{".env", "../.env", "../../.env"}
	for _, path := range envPaths {
		_ = godotenv.Load(path)
	}

	log, err := logging.New("errors.log")
	if err != nil {
		panic(err)
	}
	defer log.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Stop on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Infof("shutdown signal received")
		cancel()
	}()

	svc, err := scheduler.BuildService(ctx, log)
	if err != nil {
		log.Errorf("build service: %v", err)
		return
	}

	go func() {
		if err := svc.Run(ctx); err != nil {
			log.Errorf("scheduler stopped: %v", err)
			cancel()
		}
	}()

	b, err := bot.NewTelegramBot(svc, log, "errors.log")
	if err != nil {
		log.Errorf("bot init: %v", err)
		return
	}
	if err := b.Run(ctx); err != nil {
		log.Errorf("bot run: %v", err)
		return
	}

	<-ctx.Done()
	time.Sleep(300 * time.Millisecond)
}
