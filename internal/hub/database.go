package hub

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/0001_initial.sql
var initialMigration string

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock(hashtext('barrikade-lens-migrations'))`); err != nil {
		return err
	}
	defer connection.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('barrikade-lens-migrations'))`)
	if _, err := connection.Exec(ctx, initialMigration); err != nil {
		return fmt.Errorf("apply initial migration: %w", err)
	}
	return nil
}
