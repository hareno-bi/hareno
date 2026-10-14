package appdb_test

import (
	"context"
	"strings"
	"testing"

	"github.com/hareno-bi/hareno/internal/appdb"
	"github.com/hareno-bi/hareno/internal/appdb/appdbtest"
)

// TestRules: migrations apply once; a connection's mode and company cannot change; published
// revisions cannot change.
func TestRules(t *testing.T) {
	db := appdbtest.New(t)
	ctx := context.Background()
	if err := appdb.Migrate(ctx, db); err != nil {
		t.Fatalf("second run: %v", err)
	}
	mustFail := func(what, sql, want string) {
		t.Helper()
		if _, err := db.Exec(ctx, sql); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want an error containing %q", what, err, want)
		}
	}
	id, err := appdb.Connection(ctx, db, "mart", "ALPHA", "database-enforced")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := appdb.Connection(ctx, db, "mart", "ALPHA", "database-enforced"); err != nil || again != id {
		t.Fatalf("same connection again: %d %v", again, err)
	}
	if _, err := appdb.Connection(ctx, db, "mart", "ALPHA", "hareno"); err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Errorf("mode changed in configuration: %v", err)
	}
	mustFail("mode change", `UPDATE connection SET mode = 'hareno'`, "fixed")
	mustFail("company change", `UPDATE connection SET company = 'BETA'`, "fixed")
	mustFail("unknown mode", `INSERT INTO connection (name, company, mode) VALUES ('x', 'ALPHA', 'open')`, "check constraint")

	var report string
	if err := db.QueryRow(ctx, `INSERT INTO report (connection_id, created_by, draft) VALUES ($1, 'alice', '{}') RETURNING id`, id).Scan(&report); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO report_revision (report_id, revision, body, published_by) VALUES ($1, 1, '{}', 'alice')`, report); err != nil {
		t.Fatal(err)
	}
	mustFail("revision change", `UPDATE report_revision SET body = '{"sql": "x"}'`, "cannot be changed")
	mustFail("published points nowhere", `UPDATE report SET published = 2`, "foreign key")
	mustFail("connection with reports removed", `DELETE FROM connection`, "foreign key")
}
