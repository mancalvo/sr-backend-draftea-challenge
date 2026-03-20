package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const defaultShutdownTimeout = 10 * time.Second

// Server is the minimal HTTP server contract used by the lifecycle runner.
type Server interface {
	ListenAndServe() error
	Shutdown(ctx context.Context) error
}

// Task is a background process tied to the service lifecycle.
type Task struct {
	Name string
	Run  func(ctx context.Context) error
}

// Config describes the shared service lifecycle behavior.
type Config struct {
	ServiceName     string
	Port            int
	ShutdownTimeout time.Duration
	Logger          *slog.Logger
	Server          Server
	SignalCh        <-chan os.Signal
}

// Run starts the HTTP server and background tasks, then blocks until a signal
// or unexpected background stop triggers shutdown.
func Run(cfg Config, tasks ...Task) error {
	if cfg.Logger == nil {
		return errors.New("lifecycle logger is required")
	}
	if cfg.Server == nil {
		return errors.New("lifecycle server is required")
	}
	if cfg.ServiceName == "" {
		return errors.New("service name is required")
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = defaultShutdownTimeout
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := cfg.SignalCh
	stopSignals := func() {}
	if sigCh == nil {
		managedSigCh := make(chan os.Signal, 1)
		signal.Notify(managedSigCh, syscall.SIGINT, syscall.SIGTERM)
		sigCh = managedSigCh
		stopSignals = func() {
			signal.Stop(managedSigCh)
		}
	}
	defer stopSignals()

	var wg sync.WaitGroup

	for _, task := range tasks {
		task := task
		wg.Add(1)
		go func() {
			defer wg.Done()

			err := task.Run(ctx)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				cfg.Logger.Error("background task stopped unexpectedly", "task", task.Name, "error", err)
			} else {
				cfg.Logger.Warn("background task stopped unexpectedly", "task", task.Name)
			}
			cancel()
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		cfg.Logger.Info(fmt.Sprintf("%s HTTP server starting", cfg.ServiceName), "port", cfg.Port)
		if err := cfg.Server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
			cfg.Logger.Error("HTTP server error", "error", err)
			cancel()
		}
	}()

	select {
	case sig := <-sigCh:
		cfg.Logger.Info("received signal, shutting down", "signal", sig)
	case <-ctx.Done():
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()

	if err := cfg.Server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		cfg.Logger.Error("HTTP server shutdown error", "error", err)
	}

	wg.Wait()
	cfg.Logger.Info(fmt.Sprintf("%s stopped", cfg.ServiceName))
	return nil
}
