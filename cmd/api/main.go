// Command api serves the API, applies migrations, seeds demo data, and checks health.
package main

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

	"github.com/opikdev/api-go-starter/internal/app"
	"github.com/opikdev/api-go-starter/internal/config"
	"github.com/opikdev/api-go-starter/internal/database"
	"github.com/opikdev/api-go-starter/internal/telemetry"
)

const usage = `Usage: api [serve | migrate | seed | healthcheck]

  serve        Serve the API (default)
  migrate      Apply migrations, using MIGRATION_DATABASE_URL
  seed         Add fake demo data when SEED_DEMO_DATA=true, using MIGRATION_DATABASE_URL or DATABASE_URL
  healthcheck  Exit 0 if GET /healthz on PORT succeeds`

const cleanupInterval = time.Hour

func main() {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	switch command {
	case "serve":
		err = serve(ctx, stop)
	case "migrate":
		err = migrate(ctx)
	case "seed":
		err = seed(ctx)
	case "healthcheck":
		err = healthcheck(ctx)
	case "help", "-h", "--help":
		fmt.Println(usage)
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", command, usage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	_ = lvl.UnmarshalText([]byte(level))
	return slog.New(telemetry.LogHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}

func serve(ctx context.Context, stopSignals func()) error {
	cfg, err := config.Load(nil)
	if err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)
	shutdownTelemetry := func(context.Context) error { return nil }
	if cfg.TracingEnabled {
		if shutdownTelemetry, err = telemetry.Start(ctx, cfg.ServiceName); err != nil {
			return fmt.Errorf("start telemetry: %w", err)
		}
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL, cfg.StatementTimeoutMS, cfg.DatabasePoolMax)
	if err != nil {
		return err
	}
	defer pool.Close()

	handler, err := app.New(cfg, pool, logger)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       config.MaxRequestSeconds * time.Second,
		WriteTimeout:      config.MaxRequestSeconds * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	jobsCtx, stopJobs := context.WithCancel(context.WithoutCancel(ctx))
	defer stopJobs()
	var jobs sync.WaitGroup
	jobs.Go(func() { app.RunCleanup(jobsCtx, pool, logger, cleanupInterval) })

	errs := make(chan error, 1)
	go func() {
		logger.Info("Server listening", "port", cfg.Port)
		errs <- server.ListenAndServe()
	}()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	// A second signal now stops the process at once.
	stopSignals()

	handler.Drain()
	logger.Info("Draining before shutdown", "seconds", cfg.ShutdownDrainSeconds)
	time.Sleep(time.Duration(cfg.ShutdownDrainSeconds) * time.Second)

	logger.Info("Shutting down", "timeoutSeconds", cfg.ShutdownTimeoutSeconds)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeoutSeconds)*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	stopJobs()
	jobs.Wait()
	pool.Close()
	return errors.Join(shutdownErr, shutdownTelemetry(shutdownCtx))
}

func migrate(ctx context.Context) error {
	cfg, err := config.LoadMigrate(nil)
	if err != nil {
		return err
	}
	if err := database.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	fmt.Println("Migrations applied")
	return nil
}

func seed(ctx context.Context) error {
	cfg, err := config.LoadSeed(nil)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return errors.New("refusing to seed: set SEED_DEMO_DATA=true to add the fake demo user")
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL, 30_000, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	created, err := database.Seed(ctx, pool)
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("Seeded %s with password %s\n", database.DemoEmail, database.DemoPassword)
	} else {
		fmt.Printf("Seed skipped: %s already exists\n", database.DemoEmail)
	}
	return nil
}

func healthcheck(ctx context.Context) error {
	cfg, err := config.LoadHealthcheck(nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Port)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil) //nolint:gosec // Always loopback, on a parsed port.
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request) //nolint:gosec // Always loopback, on a parsed port.
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", response.StatusCode)
	}
	return nil
}
