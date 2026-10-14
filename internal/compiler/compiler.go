// Package compiler drives the Malloy compile sidecar (compiler/server.ts, ARCHITECTURE-PLAN.md
// §5.2). The sidecar is stateless and has no database access, so Go owns the loop: it sends the
// model and query, answers each compiler_needs request (table and SQL schemas) from the source
// database, and calls again until SQL comes back.
package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Request is one query to compile against a model.
type Request struct {
	ModelURL   string            // the model file's URL; must be a key of Files
	Files      map[string]string // model files by URL (the model and anything it imports)
	Connection string            // connection name used in the model, e.g. "mart"
	Dialect    string            // Malloy dialect, e.g. "postgres"
	Query      string            // Malloy query text
}

// Result is compiled SQL. Malloy wraps each result row in one JSON object column; Columns lists
// its keys in order, and Types their Malloy types (e.g. {"kind": "timestamptz_type",
// "timeframe": "month"}).
type Result struct {
	SQL     string
	Columns []string
	Types   []map[string]any
}

// Schemas reports the columns of a table or of a SQL query, in Malloy's field format.
type Schemas interface {
	TableSchema(ctx context.Context, table string) ([]Field, error)
	SQLSchema(ctx context.Context, sql string) ([]Field, error)
}

// Field is a Malloy schema field.
type Field struct {
	Kind string         `json:"kind"`
	Name string         `json:"name"`
	Type map[string]any `json:"type"`
}

// Client calls the sidecar at URL, e.g. http://127.0.0.1:8090.
type Client struct {
	URL  string
	HTTP *http.Client
}

// ErrCompile is wrapped by errors Malloy reports about the model or query.
var ErrCompile = errors.New("malloy compile error")

const maxRounds = 5 // each round answers one batch of needs; real models settle in two or three

func (c *Client) Compile(ctx context.Context, req Request, schemas Schemas) (Result, error) {
	needs := map[string]any{
		"files":       []map[string]string{{"url": req.ModelURL, "contents": req.Files[req.ModelURL]}},
		"connections": []map[string]string{{"name": req.Connection, "dialect": req.Dialect}},
	}
	for range maxRounds {
		var resp struct {
			Logs []struct {
				Severity string `json:"severity"`
				Message  string `json:"message"`
			} `json:"logs"`
			Result *struct {
				SQL    string `json:"sql"`
				Schema struct {
					Fields []struct {
						Name string         `json:"name"`
						Type map[string]any `json:"type"`
					} `json:"fields"`
				} `json:"schema"`
			} `json:"result"`
			Needs struct {
				Files        []map[string]any `json:"files"`
				TableSchemas []map[string]any `json:"table_schemas"`
				SQLSchemas   []map[string]any `json:"sql_schemas"`
			} `json:"compiler_needs"`
		}
		if err := c.post(ctx, map[string]any{"model_url": req.ModelURL, "query_malloy": req.Query, "compiler_needs": needs}, &resp); err != nil {
			return Result{}, err
		}
		var errs []string
		for _, l := range resp.Logs {
			if l.Severity == "error" {
				errs = append(errs, l.Message)
			}
		}
		if len(errs) > 0 {
			return Result{}, fmt.Errorf("%w: %s", ErrCompile, strings.Join(errs, "; "))
		}
		if resp.Result != nil {
			r := Result{SQL: resp.Result.SQL}
			for _, f := range resp.Result.Schema.Fields {
				r.Columns = append(r.Columns, f.Name)
				r.Types = append(r.Types, f.Type)
			}
			return r, nil
		}
		answered := false
		for _, f := range resp.Needs.Files {
			url, _ := f["url"].(string)
			body, ok := req.Files[url]
			if !ok {
				return Result{}, fmt.Errorf("%w: model imports unknown file %q", ErrCompile, url)
			}
			needs["files"] = append(needs["files"].([]map[string]string), map[string]string{"url": url, "contents": body})
			answered = true
		}
		for _, t := range resp.Needs.TableSchemas {
			name, _ := t["name"].(string)
			fields, err := schemas.TableSchema(ctx, name)
			if err != nil {
				return Result{}, fmt.Errorf("schema of table %s: %w", name, err)
			}
			t["schema"] = map[string]any{"fields": fields}
			needs["table_schemas"] = append(asList(needs["table_schemas"]), t)
			answered = true
		}
		for _, s := range resp.Needs.SQLSchemas {
			sql, _ := s["sql"].(string)
			fields, err := schemas.SQLSchema(ctx, sql)
			if err != nil {
				return Result{}, fmt.Errorf("schema of SQL source: %w", err)
			}
			s["schema"] = map[string]any{"fields": fields}
			needs["sql_schemas"] = append(asList(needs["sql_schemas"]), s)
			answered = true
		}
		if !answered {
			return Result{}, errors.New("sidecar returned neither SQL nor needs")
		}
	}
	return Result{}, errors.New("compile loop did not converge")
}

func asList(v any) []map[string]any {
	l, _ := v.([]map[string]any)
	return l
}

func (c *Client) post(ctx context.Context, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/compile", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	h := c.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	resp, err := h.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("sidecar: %s: %s", resp.Status, e.Error)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
