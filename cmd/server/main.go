package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"baccarat-live-simulator/internal/config"
	"baccarat-live-simulator/internal/httpapi"
	"baccarat-live-simulator/internal/redact"
	"baccarat-live-simulator/internal/state"
	"baccarat-live-simulator/internal/store"
	"baccarat-live-simulator/internal/ws"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration", "err", redact.Error(err))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database", "err", redact.Error(err))
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		logger.Error("migrate", "err", redact.Error(err))
		os.Exit(1)
	}

	mgr := state.New(db, logger)
	if err := mgr.Load(ctx); err != nil {
		logger.Error("load state", "err", redact.Error(err))
		os.Exit(1)
	}
	hub := ws.New(logger)
	mgr.SetPublisher(hub)
	srv := httpapi.New(cfg.Addr, mgr, hub, logger)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server", "err", redact.Error(err))
		}
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	mgr.Stop()
	hub.Close()
	if err := srv.Shutdown(shutCtx); err != nil {
		logger.Error("shutdown", "err", redact.Error(err))
	}
}
