// Package query runs Malloy queries: compile through the sidecar, execute on the source with the
// connection's restricted credential, and return typed rows.
package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hareno-bi/hareno/internal/compiler"
)

// Model is a Malloy model and the connection it reads.
type Model struct {
	URL        string            // the model file's URL, a key of Files
	Files      map[string]string // model files by URL
	Connection string            // the connection name the model uses, e.g. "mart"
	Dialect    string            // e.g. "postgres"
}

// Runner executes queries against one model and one PostgreSQL source. DB must use the
// connection's restricted read credential.
type Runner struct {
	Compiler *compiler.Client
	DB       *pgxpool.Pool
	Model    Model
}

// Value is a result value: json.Number (numbers keep their exact text), string, bool or nil.
// Dates and timestamps are strings as PostgreSQL renders them in JSON.
type Value = any

// Result is a query's output.
type Result struct {
	SQL     string
	Columns []string
	Rows    [][]Value
}

// Run compiles and executes one Malloy query.
func (r Runner) Run(ctx context.Context, malloy string) (Result, error) {
	c, err := r.Compiler.Compile(ctx, compiler.Request{
		ModelURL: r.Model.URL, Files: r.Model.Files, Connection: r.Model.Connection, Dialect: r.Model.Dialect, Query: malloy,
	}, compiler.Postgres{DB: r.DB})
	if err != nil {
		return Result{}, err
	}
	res := Result{SQL: c.SQL, Columns: c.Columns}
	rows, err := r.DB.Query(ctx, c.SQL)
	if err != nil {
		return res, fmt.Errorf("execute: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		// Malloy wraps each row in one JSON object column (row_to_json).
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return res, err
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var obj map[string]Value
		if err := d.Decode(&obj); err != nil {
			return res, fmt.Errorf("decode row: %w", err)
		}
		row := make([]Value, len(c.Columns))
		for i, col := range c.Columns {
			row[i] = obj[col]
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return res, fmt.Errorf("execute: %w", err)
	}
	return res, nil
}

// Objects returns the rows as maps keyed by column name.
func (r Result) Objects() []map[string]Value {
	out := make([]map[string]Value, len(r.Rows))
	for i, row := range r.Rows {
		m := make(map[string]Value, len(r.Columns))
		for j, c := range r.Columns {
			m[c] = row[j]
		}
		out[i] = m
	}
	return out
}
