package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"hasgier/internal/config"
	"hasgier/internal/db"
	"hasgier/internal/integrations/quickbooks"
	"hasgier/internal/integrations/shipstation"
	"hasgier/internal/integrations/woocommerce"
	"hasgier/internal/webhook"
	"hasgier/internal/workers"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Run migrations
	m, err := migrate.New("file://internal/db/migrations", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}
	m.Close()

	// Create database pool
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()

	// Create river client
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Workers: workers.NewWorkers(),
	})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}
	defer riverClient.Stop(ctx)

	// Create integration clients
	wooClient := woocommerce.NewClient(cfg.WooURL, cfg.WooConsumerKey, cfg.WooConsumerSecret)
	ssClient := shipstation.NewClient(cfg.ShipStationAPIKey, cfg.ShipStationAPISecret)
	qbOAuth := &quickbooks.OAuthConfig{
		ClientID:     cfg.QBClientID,
		ClientSecret: cfg.QBClientSecret,
		RedirectURI:  cfg.QBRedirectURI,
		Environment:  cfg.QBEnvironment,
	}

	// Create QB client with token provider
	qbClient := quickbooks.NewClient(qbOAuth, func(ctx context.Context) (string, error) {
		// TODO: implement token refresh logic
		return "", fmt.Errorf("not implemented")
	}, "")

	_ = wooClient
	_ = ssClient
	_ = qbClient

	// Create webhook context
	webhookCtx := &webhook.Context{
		Config: cfg,
		Pool:   pool,
		River:  riverClient,
	}

	// Create HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      webhook.Router(webhookCtx),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start river client
	if err := riverClient.Start(ctx); err != nil {
		return fmt.Errorf("start river: %w", err)
	}

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		log.Printf("listening on :%d", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errChan:
		return fmt.Errorf("server error: %w", err)
	case sig := <-sigChan:
		log.Printf("received %s, shutting down", sig)
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown server: %w", err)
	}

	if err := riverClient.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("stop river: %w", err)
	}

	return nil
}
