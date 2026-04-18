// Package db owns the embedded Postgres cluster and repositories.
// Shared() returns nil until Start() has finished.
package db

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	pgUser     = "velocity"
	pgPassword = "velocity"
	pgDatabase = "velocity"
	pgVersion  = embeddedpostgres.V16
)

var (
	mu     sync.RWMutex
	pg     *embeddedpostgres.EmbeddedPostgres
	pool   *pgxpool.Pool
	port   uint32
	dataOK bool
)

// Start boots the cluster, opens the pool, and runs CREATE-TABLE DDL.
// Safe to call twice — the second call is a no-op.
func Start(ctx context.Context, dataDir string) error {
	mu.Lock()
	defer mu.Unlock()
	if dataOK {
		return nil
	}

	p, err := pickFreePort()
	if err != nil {
		return fmt.Errorf("pick port: %w", err)
	}

	cfg := embeddedpostgres.DefaultConfig().
		Version(pgVersion).
		Username(pgUser).
		Password(pgPassword).
		Database(pgDatabase).
		Port(p).
		BinariesPath(filepath.Join(dataDir, "binaries")).
		DataPath(filepath.Join(dataDir, "pgdata")).
		RuntimePath(filepath.Join(dataDir, "runtime")).
		CachePath(filepath.Join(dataDir, "cache")).
		StartTimeout(90 * time.Second)

	inst := embeddedpostgres.NewDatabase(cfg)
	if err := inst.Start(); err != nil {
		return fmt.Errorf("start embedded postgres: %w", err)
	}

	dsn := fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable",
		pgUser, pgPassword, p, pgDatabase)

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_ = inst.Stop()
		return fmt.Errorf("parse pool config: %w", err)
	}
	poolCfg.MaxConns = 8

	poolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pl, err := pgxpool.NewWithConfig(poolCtx, poolCfg)
	if err != nil {
		_ = inst.Stop()
		return fmt.Errorf("open pool: %w", err)
	}
	if err := pl.Ping(poolCtx); err != nil {
		pl.Close()
		_ = inst.Stop()
		return fmt.Errorf("ping pool: %w", err)
	}

	if err := migrate(poolCtx, pl); err != nil {
		pl.Close()
		_ = inst.Stop()
		return fmt.Errorf("migrate: %w", err)
	}

	pg = inst
	pool = pl
	port = p
	dataOK = true
	return nil
}

// Stop tears down the pool and cluster. Safe to call twice.
func Stop() error {
	mu.Lock()
	defer mu.Unlock()
	if !dataOK {
		return nil
	}
	if pool != nil {
		pool.Close()
	}
	var err error
	if pg != nil {
		err = pg.Stop()
	}
	pool = nil
	pg = nil
	dataOK = false
	return err
}

// Shared returns the package-level pool; nil until Start has finished.
func Shared() *pgxpool.Pool {
	mu.RLock()
	defer mu.RUnlock()
	return pool
}

// ErrNotStarted means a repo call was made before Start.
var ErrNotStarted = errors.New("db: not started")

func pickFreePort() (uint32, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}
