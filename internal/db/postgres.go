// Package db owns the external Postgres connection pool and repositories.
// Shared() returns nil until Start() has finished.
package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/randheer094/velocity/internal/config"
)

var (
	mu     sync.RWMutex
	pool   *pgxpool.Pool
	dataOK bool
)

// Start opens the pool against the DB named in cfg (host + password
// from DBHostEnv / DBPasswordEnv) and runs any unapplied migrations.
// Safe to call twice — the second call is a no-op.
func Start(ctx context.Context, cfg config.DatabaseConfig) error {
	mu.Lock()
	defer mu.Unlock()
	if dataOK {
		return nil
	}

	host := os.Getenv(config.DBHostEnv)
	if host == "" {
		return fmt.Errorf("%s must be exported", config.DBHostEnv)
	}
	password := os.Getenv(config.DBPasswordEnv)
	if password == "" {
		return fmt.Errorf("%s must be exported", config.DBPasswordEnv)
	}

	port := cfg.Port
	if v := os.Getenv(config.DBPortEnv); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p <= 0 || p > 65535 {
			return fmt.Errorf("%s is not a valid port: %q", config.DBPortEnv, v)
		}
		port = p
	}

	dsn := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, password),
		Host:     fmt.Sprintf("%s:%d", host, port),
		Path:     "/" + cfg.Name,
		RawQuery: "sslmode=" + url.QueryEscape(cfg.SSLMode),
	}).String()

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse pool config: %w", err)
	}
	poolCfg.MaxConns = 8

	poolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pl, err := pgxpool.NewWithConfig(poolCtx, poolCfg)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	if err := pl.Ping(poolCtx); err != nil {
		pl.Close()
		return fmt.Errorf("ping pool: %w", err)
	}

	if err := migrate(poolCtx, pl); err != nil {
		pl.Close()
		return fmt.Errorf("migrate: %w", err)
	}

	pool = pl
	dataOK = true
	return nil
}

// Stop closes the pool. Safe to call twice.
func Stop() error {
	mu.Lock()
	defer mu.Unlock()
	if !dataOK {
		return nil
	}
	if pool != nil {
		pool.Close()
	}
	pool = nil
	dataOK = false
	return nil
}

// Shared returns the package-level pool; nil until Start has finished.
func Shared() *pgxpool.Pool {
	mu.RLock()
	defer mu.RUnlock()
	return pool
}

// ErrNotStarted means a repo call was made before Start.
var ErrNotStarted = errors.New("db: not started")
