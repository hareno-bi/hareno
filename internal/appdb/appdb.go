// Package appdb is Hareno's application database: its schema migrations, applied in order at
// startup.
package appdb

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies the migrations db has not seen, each in its own transaction. Replicas starting
// together wait on one advisory lock, so each migration runs once.
func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	slices.Sort(names) // NNN_name.sql
	conn, err := db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	const lock = 7_310_001 // arbitrary, Hareno's
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lock); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lock)
	if _, err := conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migration (version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return err
	}
	for _, name := range names {
		base := strings.TrimPrefix(name, "migrations/")
		v, err := strconv.Atoi(strings.SplitN(base, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a number", base)
		}
		sql, _ := migrations.ReadFile(name)
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, "INSERT INTO schema_migration (version) VALUES ($1) ON CONFLICT DO NOTHING", v)
			if err != nil || tag.RowsAffected() == 0 {
				return err // applied before
			}
			_, err = tx.Exec(ctx, string(sql))
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", base, err)
		}
	}
	return nil
}

// Connection records a configured connection the first time it is seen and returns its id. A
// connection seen before with another company or data access mode is refused: the mode is fixed
// for the connection's life (ARCHITECTURE-PLAN §5.3).
func Connection(ctx context.Context, db *pgxpool.Pool, name, company, mode string) (int64, error) {
	if _, err := db.Exec(ctx, `INSERT INTO connection (name, company, mode) VALUES ($1, $2, $3) ON CONFLICT (name) DO NOTHING`,
		name, company, mode); err != nil {
		return 0, err
	}
	var id int64
	var was, wasCompany string
	if err := db.QueryRow(ctx, `SELECT id, mode, company FROM connection WHERE name = $1`, name).Scan(&id, &was, &wasCompany); err != nil {
		return 0, err
	}
	if was != mode || wasCompany != company {
		return 0, fmt.Errorf("connection %s was added for company %s in mode %s; company and mode cannot change. "+
			"Remove the connection (and its reports) and add it again, or add it under a new name", name, wasCompany, was)
	}
	return id, nil
}
