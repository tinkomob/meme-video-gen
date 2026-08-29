package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"meme-video-gen/internal/bot"
	"meme-video-gen/internal/friends"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/scheduler"
	"meme-video-gen/internal/web"

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

	friendsHandler := web.NewFriendsHandler(friends.New(svc.GetS3Client(), []int{1}), log)
	mux := http.NewServeMux()
	friendsHandler.Register(mux)
	server := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		log.Infof("Friends player is listening on http://localhost:8080/friends")
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Errorf("web server: %v", serveErr)
			cancel()
		}
	}()

	// Telegram is an optional network-dependent part of the process. Do not
	// stop the HTTP server and scheduler when Telegram is temporarily
	// unreachable; keep retrying until the application is shut down.
	go runTelegramBot(ctx, svc, log, cancel)

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Errorf("web server shutdown: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
}

func runTelegramBot(ctx context.Context, svc *scheduler.Service, log *logging.Logger, cancel context.CancelFunc) {
	const (
		maxAttempts = 5
		retryDelay  = 10 * time.Second
	)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return
		}

		b, err := bot.NewTelegramBot(svc, log, "errors.log", cancel)
		if err != nil {
			if attempt == maxAttempts {
				log.Errorf("bot init failed after %d attempts: %v; continuing without Telegram", maxAttempts, err)
				return
			}
			log.Errorf("bot init attempt %d/%d failed: %v; retrying in %s", attempt, maxAttempts, err, retryDelay)
			if !waitForRetry(ctx, retryDelay) {
				return
			}
			continue
		}

		if err := b.Run(ctx); err != nil && ctx.Err() == nil {
			if attempt == maxAttempts {
				log.Errorf("bot run failed after %d attempts: %v; continuing without Telegram", maxAttempts, err)
				return
			}
			log.Errorf("bot run attempt %d/%d failed: %v; retrying in %s", attempt, maxAttempts, err, retryDelay)
		}
		if ctx.Err() != nil || !waitForRetry(ctx, retryDelay) {
			return
		}
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
