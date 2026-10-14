// Package sqlexec runs SQL typed by a user (the SQL editor, ARCHITECTURE-PLAN §5.3) on a
// connection in "database-enforced" mode. Row security is the database's: Hareno only sets the
// viewer's scope and drops into a role that cannot change it. Per statement:
//
//	BEGIN READ ONLY (as the gate login)
//	set_config('hareno.<attr>', '<JSON array>', true) for each scope attribute
//	SET LOCAL statement_timeout
//	SET LOCAL ROLE <sql role>        -- cannot call set_config, so cannot widen its scope
//	prepare the statement; refuse it unless it returns rows (SELECT, VALUES, WITH ... SELECT)
//	execute, reading at most MaxRows
//	ROLLBACK
//
// The extended query protocol accepts exactly one statement, and the read-only transaction
// refuses writes; the database's privileges and policies remain the trust boundary (§4.1).
package sqlexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exec runs user SQL. DB must log in as the connection's gate role.
type Exec struct {
	DB      *pgxpool.Pool
	Role    string        // the restricted role user SQL runs as, e.g. hareno_sql
	Timeout time.Duration // per statement
	MaxRows int           // rows returned at most; Result.Truncated reports more
}

// Scope is the viewer's data scope: attribute -> allowed values, e.g. {"region": ["North"]}.
// A value of "*" allows all. Attributes without values allow nothing.
type Scope map[string][]string

// Result is a query result. Values are JSON-ready: NUMERIC becomes json.Number with its exact
// decimal text; other values are as pgx decodes them.
type Result struct {
	Columns   []string
	Rows      [][]any
	Truncated bool
}

// ErrNotQuery is returned for statements that do not return rows.
var ErrNotQuery = errors.New("only a single query that returns rows is allowed")

var attr = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func (e Exec) Run(ctx context.Context, scope Scope, sql string) (res Result, err error) {
	res.Truncated, err = e.Each(ctx, scope, sql, func(cols []string) error {
		res.Columns = cols
		return nil
	}, func(row []any) error {
		res.Rows = append(res.Rows, row)
		return nil
	})
	return res, err
}

// Each runs sql like Run, but hands over the columns and then each row as it arrives instead of
// keeping them: for results too big to hold, such as exports. It reports whether rows remained
// past MaxRows. An error from cols or row stops the query and is returned.
func (e Exec) Each(ctx context.Context, scope Scope, sql string, cols func([]string) error, row func([]any) error) (truncated bool, err error) {
	if !attr.MatchString(e.Role) {
		return false, fmt.Errorf("invalid role %q", e.Role)
	}
	if e.Timeout <= 0 || e.MaxRows <= 0 {
		return false, errors.New("timeout and row limit are required") // statement_timeout 0 means none
	}
	tx, err := e.DB.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	for k, vals := range scope {
		if !attr.MatchString(k) {
			return false, fmt.Errorf("invalid scope attribute %q", k)
		}
		b, _ := json.Marshal(vals)
		if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", "hareno."+k, string(b)); err != nil {
			return false, fmt.Errorf("set scope: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", e.Timeout.Milliseconds())); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+e.Role); err != nil {
		return false, err
	}

	desc, err := tx.Prepare(ctx, "", sql)
	if err != nil {
		return false, err
	}
	if len(desc.Fields) == 0 {
		return false, ErrNotQuery
	}
	rows, err := tx.Query(ctx, desc.SQL)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var names []string
	for _, f := range rows.FieldDescriptions() {
		names = append(names, f.Name)
	}
	if err := cols(names); err != nil {
		return false, err
	}
	for n := 0; rows.Next(); n++ {
		if n == e.MaxRows {
			return true, nil
		}
		vals, err := rows.Values()
		if err != nil {
			return false, err
		}
		for i, v := range vals {
			if n, ok := v.(pgtype.Numeric); ok {
				vals[i] = numeric(n)
			}
		}
		if err := row(vals); err != nil {
			return false, err
		}
	}
	return false, rows.Err()
}

func numeric(n pgtype.Numeric) any {
	if !n.Valid {
		return nil
	}
	v, err := n.Value() // exact decimal text, or NaN/Infinity
	if s, ok := v.(string); ok && err == nil {
		if _, ok := new(big.Rat).SetString(s); ok {
			return json.Number(s)
		}
		return s
	}
	return nil
}
