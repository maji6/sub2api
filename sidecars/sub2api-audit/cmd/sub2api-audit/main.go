package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api-audit/internal/app"
	"github.com/Wei-Shaw/sub2api-audit/internal/config"
)

func main() {
	configPath := flag.String("config", "", "Path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application, err := app.New(rootCtx, cfg)
	if err != nil {
		log.Fatalf("initialize app: %v", err)
	}
	defer func() {
		if closeErr := application.Close(); closeErr != nil {
			log.Printf("close app: %v", closeErr)
		}
	}()

	errCh := make(chan error, 2)
	if cfg.Audit.ConsumerEnabled {
		go func() {
			if runErr := application.Consumer.Run(rootCtx); runErr != nil && !errors.Is(runErr, context.Canceled) {
				errCh <- runErr
			}
		}()
	}

	go func() {
		log.Printf("sub2api-audit listening on %s", application.Server.Addr)
		if serveErr := application.Server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()

	select {
	case <-rootCtx.Done():
	case runErr := <-errCh:
		log.Printf("runtime error: %v", runErr)
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := application.Server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown http server: %v", err)
	}

	if rootCtx.Err() != nil && !errors.Is(rootCtx.Err(), context.Canceled) {
		log.Printf("stopped with context error: %v", rootCtx.Err())
	}
	os.Exit(0)
}
