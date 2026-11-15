// Package pg opens Postgres with retries and runs embedded migrations. Each service keeps its own migrations table,
// so two services may share one database without stepping on each other.
package pg

import (
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open connects, retrying until ctx ends (the database may still be starting).
func Open(ctx context.Context, url string, maxConns int32, log func(string, ...any)) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = maxConns
	delay := time.Second
	for {
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
		}
		log("database not ready, retrying", "err", err, "in", delay.String())
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("database: %w", err)
		case <-time.After(delay):
		}
		delay = min(delay*2, 30*time.Second)
	}
}

// Migrate applies the *.sql files in files (sorted by name) that have not run yet, each in a transaction, and
// records them in table.
func Migrate(ctx context.Context, db *pgxpool.Pool, files fs.FS, table string) error {
	if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+pgx.Identifier{table}.Sanitize()+` (name text PRIMARY KEY, at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	h := fnv.New32a()
	h.Write([]byte(table))
	lock := int64(h.Sum32())
	tbl := pgx.Identifier{table}.Sanitize()
	for _, n := range names {
		sql, err := fs.ReadFile(files, n)
		if err != nil {
			return err
		}
		err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
			// serialise migrations when several instances start at once
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lock); err != nil {
				return err
			}
			var done bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+tbl+` WHERE name = $1)`, n).Scan(&done); err != nil || done {
				return err
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("migration %s: %w", n, err)
			}
			_, err := tx.Exec(ctx, `INSERT INTO `+tbl+` (name) VALUES ($1)`, n)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
