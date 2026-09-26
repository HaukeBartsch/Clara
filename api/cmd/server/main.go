// Command server is the Go API entry point: config → migrations → routes
// (Technology_Stack_Design.md §4/§5). It fails fast on invalid configuration
// (REQ-CFG-004) or an unreachable database (REQ-TECH-014), applies pending
// migrations at startup (REQ-DB-003), and serves the data API at /api/ plus
// the health endpoint (REQ-API-003). The administration surface (/api/v1/)
// and the documentation endpoints are added as their handlers land; nginx
// keeps them off the public network (REQ-TECH-018).
package main

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

	"golang.org/x/crypto/bcrypt"

	"csms/api/internal/audit"
	"csms/api/internal/config"
	"csms/api/internal/db"
	"csms/api/internal/httpapi"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err // names every missing/invalid variable (REQ-CFG-004)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel(cfg.LogLevelAPI),
	}))
	slog.SetDefault(logger)

	// Startup dump of the effective configuration with secrets masked
	// (REQ-CFG-021/022).
	for _, line := range cfg.Masked().Dump() {
		logger.Info("config", "value", line)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := db.Open(cfg) // fail fast on an unreachable database (REQ-TECH-014)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrations: %w", err) // REQ-DB-003
	}
	version, err := store.SchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("schema version: %w", err)
	}

	if err := bootstrapAdmin(ctx, store, cfg); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}

	// Audit year objects (partitions / per-year tables) before serving
	// (Audit_Logging_Design.md §6).
	aw := audit.NewWriter(store.DB, string(store.Dialect))
	if err := aw.EnsureYear(ctx); err != nil {
		return fmt.Errorf("audit rollover: %w", err)
	}

	mux := httpapi.NewMux(store, cfg, aw)

	srv := &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.APIAddr, "env", cfg.AppEnv,
			"db", cfg.DBConnection, "schema_version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	// Graceful shutdown: stop accepting, drain in-flight requests.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	logger.Info("stopped")
	return nil
}

// bootstrapAdmin ensures the bootstrap administrator exists when configured
// (GD-4, GD-18, REQ-CFG-014/025). The plaintext password from
// ADMIN_BOOTSTRAP_PASSWORD is hashed with bcrypt (cost ≥ 10) and never
// logged; an existing row keeps its current hash — password changes go
// through the administration API (REQ-AUTH-050).
func bootstrapAdmin(ctx context.Context, store *db.Store, cfg *config.Config) error {
	if cfg.AdminBootstrapEmail == "" || cfg.AdminBootstrapPassword == "" {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminBootstrapPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u, err := store.UpsertBootstrap(ctx, cfg.AdminBootstrapEmail, "Administrator", string(hash))
	if err != nil {
		return err
	}
	slog.Info("bootstrap admin ready", "email", u.Email, "user_id", u.ID)
	return nil
}

// logLevel maps the validated LOG_LEVEL_API value to a slog level.
func logLevel(name string) slog.Level {
	switch name {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
