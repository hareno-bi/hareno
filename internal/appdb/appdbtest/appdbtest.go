// Package appdbtest gives integration tests a fresh, migrated application database on the dev
// app DB (docker compose up -d appdb), dropped when the test ends.
package appdbtest

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hareno-bi/hareno/internal/appdb"
	"github.com/hareno-bi/hareno/internal/compiler/compilertest"
)

const server = "postgres://hareno:dev@localhost:55434/"

func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	compilertest.Require(t)
	ctx := context.Background()
	a, err := pgxpool.New(ctx, server+"hareno")
	if err != nil {
		t.Fatal(err)
	}
	name := "test_" + strings.ToLower(rand.Text()[:12])
	if _, err := a.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	db, err := pgxpool.New(ctx, server+name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		a.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		a.Close()
	})
	if err := appdb.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}
