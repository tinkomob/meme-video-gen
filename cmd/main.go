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

	b, err := bot.NewTelegramBot(svc, log, "errors.log", cancel)
	if err != nil {
		log.Errorf("bot init: %v", err)
		return
	}
	go func() {
		if runErr := b.Run(ctx); runErr != nil {
			log.Errorf("bot run: %v", runErr)
			cancel()
		}
	}()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Errorf("web server shutdown: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
}
