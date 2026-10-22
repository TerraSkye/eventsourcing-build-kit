// Command app serves the app slices over HTTP.
//
// esgen wrote this file once; it is yours. The slices are wired in
// app_gen.go, the backend in wiring.go.
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
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if err := serve(ctx, stop, logger); err != nil {
		logger.Error("stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// serve runs the service until ctx is done or a background process fails.
func serve(ctx context.Context, stop func(), logger *slog.Logger) error {
	inf, err := openInfra(ctx, getenv("DATABASE", "events.db"))
	if err != nil {
		return err
	}
	defer inf.Close()

	a := newApp(logger, inf)
	defer a.close()

	srv := &http.Server{Addr: getenv("ADDR", ":8080"), Handler: a.routes}
	go func() {
		if err := a.run(ctx); err != nil {
			logger.Error("background process failed", slog.String("error", err.Error()))
		}
		stop() // a failed background process takes the server down with it
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx) // the server is stopping either way
	}()

	logger.Info("listening", slog.String("addr", srv.Addr))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func getenv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
