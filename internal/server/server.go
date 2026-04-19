// Package server runs the HTTP mux with graceful shutdown.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/db"
	"github.com/randheer094/velocity/internal/jira"
	"github.com/randheer094/velocity/internal/webhook"
)

// Run blocks until SIGINT/SIGTERM.
func Run() error {
	cfg := config.Get()
	if cfg == nil {
		return fmt.Errorf("config not loaded: %s", config.LoadError())
	}
	if err := config.EnsureRuntimeDirs(); err != nil {
		return fmt.Errorf("ensure runtime dirs: %w", err)
	}
	jira.Reinit()

	startCtx, cancelStart := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelStart()
	if err := db.Start(startCtx); err != nil {
		return fmt.Errorf("start db: %w", err)
	}
	defer func() {
		if err := db.Stop(); err != nil {
			slog.Error("db stop failed", "err", err)
		}
	}()

	queueCtx, cancelQueue := context.WithCancel(context.Background())
	defer cancelQueue()
	webhook.Start(queueCtx, cfg.Server.MaxConcurrency, cfg.Server.QueueSize)

	mux := http.NewServeMux()
	mux.Handle("POST /webhook/jira", webhook.JiraHandler{})
	mux.Handle("POST /webhook/github", webhook.GithubHandler{})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("velocity listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-stop:
		slog.Info("velocity shutdown signal", "sig", sig.String())
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	webhook.Drain(shutdownCtx)
	return shutdownErr
}
