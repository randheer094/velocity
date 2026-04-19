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

// Start opens the pool against the external Postgres. All connection
// fields come from env vars (DBHostEnv / DBPortEnv / DBUserEnv /
// DBPasswordEnv / DBNameEnv). sslmode is always `disable`. Safe to
// call twice — the second call is a no-op.
func Start(ctx context.Context) error {
	mu.Lock()
	defer mu.Unlock()
	if dataOK {
		return nil
	}

	host, err := requireEnv(config.DBHostEnv)
	if err != nil {
		return err
	}
	user, err := requireEnv(config.DBUserEnv)
	if err != nil {
		return err
	}
	password, err := requireEnv(config.DBPasswordEnv)
	if err != nil {
		return err
	}
	name, err := requireEnv(config.DBNameEnv)
	if err != nil {
		return err
	}
	portStr, err := requireEnv(config.DBPortEnv)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("%s is not a valid port: %q", config.DBPortEnv, portStr)
	}

	dsn := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, password),
		Host:     fmt.Sprintf("%s:%d", host, port),
		Path:     "/" + name,
		RawQuery: "sslmode=disable",
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

func requireEnv(name string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("%s must be exported", name)
	}
	return v, nil
}
