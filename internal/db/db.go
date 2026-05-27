// Package db wraps a pgx connection pool with the platform's invariants:
// every query carries a tenant context, and the pool is closeable for graceful shutdown.
package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is the platform's database handle. A single instance is shared per service.
type Pool struct {
	*pgxpool.Pool
}

// Open creates a connection pool from a Postgres connection URL.
func Open(ctx context.Context, url string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Pool{Pool: pool}, nil
}

// Close releases the pool.
func (p *Pool) Close() {
	if p == nil || p.Pool == nil {
		return
	}
	p.Pool.Close()
}

// ErrNotFound is returned by repository lookups when no row matched.
var ErrNotFound = errors.New("not found")
