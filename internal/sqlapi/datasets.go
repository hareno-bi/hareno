package sqlapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hareno-bi/hareno/internal/authz"
	"github.com/hareno-bi/hareno/internal/compiler"
	"github.com/hareno-bi/hareno/internal/model"
)

// Dataset tiles (P2-09; ARCHITECTURE-PLAN §5, §10.4 item 21). A tile may be built from a governed
// dataset instead of SQL: it names the dataset, the dimensions to group by and the measures to
// show. What is saved is those names, never SQL. Each time the tile runs, Hareno plans the query
// from the dataset's model (internal/model: guarded measures, grain check), compiles it to SQL
// through the Malloy sidecar, and runs that SQL exactly as a SQL tile's: under the viewer's row
// policy, with filters, matrix totals, the results cache and CSV downloads unchanged.
//
//	GET  /api/datasets   the datasets the user may build tiles from, with their fields
//	POST /api/sql        {"connection", "dataset": {...}} previews a dataset tile (sqlapi.go)
//
// Building from datasets needs connection#dataset_user, or sql_user (which allows more).
type DatasetQuery struct {
	Dataset    string   `json:"dataset"`
	Dimensions []string `json:"dimensions"`
	Measures   []string `json:"measures"`
	// Where holds Malloy conditions Hareno adds itself (a drill-down, drill.go), never the client's.
	Where []string `json:"-"`
}

func (q *DatasetQuery) valid() bool {
	if q.Dataset == "" || len(q.Dimensions) > 10 || len(q.Measures) > 10 || len(q.Dimensions)+len(q.Measures) == 0 {
		return false
	}
	names := append(slices.Clone(q.Dimensions), q.Measures...)
	for i, n := range names {
		if !validColumn(n) || slices.Contains(names[:i], n) {
			return false
		}
	}
	return true
}

// Datasets are the governed datasets, by name, and the compiler for their queries.
type Datasets struct {
	Compiler *compiler.Client
	ByName   map[string]*model.Dataset

	mu       sync.Mutex
	compiled map[string]compiled // dataset and planned Malloy -> SQL
}

type compiled struct {
	sql  string
	text []string // the columns holding text: the dimensions a filter can apply to
}

// ErrDataset is wrapped by errors in a dataset tile's choices (unknown names, a measure split
// below its grain), which the user can fix.
var ErrDataset = errors.New("dataset query")

// compile is q as SQL on the connection named connection: ordinary columns named after the
// dimensions and measures, in dimension order. Compiled SQL is kept: the model only changes on
// restart.
func (ds *Datasets) compile(ctx context.Context, connection string, conn Connection, name string, q model.Query) (compiled, error) {
	d, ok := ds.ByName[name]
	if !ok || d.Connection != connection {
		return compiled{}, fmt.Errorf("%w: no dataset %q on this connection", ErrDataset, name)
	}
	malloy, err := d.Plan(q)
	if err != nil {
		return compiled{}, fmt.Errorf("%w: %v", ErrDataset, err)
	}
	key := name + "\x00" + malloy
	ds.mu.Lock()
	c, ok := ds.compiled[key]
	ds.mu.Unlock()
	if ok {
		return c, nil
	}
	url := "file://hareno/" + d.Name + ".malloy"
	res, err := ds.Compiler.Compile(ctx, compiler.Request{
		ModelURL: url, Files: map[string]string{url: d.Malloy()}, Connection: d.Connection, Dialect: "postgres", Query: malloy,
	}, compiler.Postgres{DB: conn.Exec.DB})
	if err != nil {
		return compiled{}, err
	}
	c = compiled{sql: unwrap(res, d.TimeZone, len(q.Dimensions))}
	for i, col := range res.Columns {
		if kind, _ := res.Types[i]["kind"].(string); kind == "string_type" {
			c.text = append(c.text, col)
		}
	}
	ds.mu.Lock()
	if ds.compiled == nil {
		ds.compiled = map[string]compiled{}
	}
	ds.compiled[key] = c
	ds.mu.Unlock()
	return c, nil
}

// where is the filters on columns as Malloy conditions, applied before the measures are computed.
func where(f Filters, columns []string) []string {
	var out []string
	for _, c := range columns {
		var or []string
		for _, v := range f[c] {
			or = append(or, c+" = "+malloyString(v))
		}
		out = append(out, "("+strings.Join(or, " or ")+")")
	}
	return out
}

var malloyEscapes = strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

func malloyString(v string) string { return "'" + malloyEscapes.Replace(v) + "'" }

// tile is the SQL a dataset tile runs, narrowed by the filters that apply to it (its text
// dimensions) and the date range (on the dataset's date, dates.go) and, with a matrix, as the
// matrix's rows with every total. It returns the filters applied, and whether the dates were.
//
// A matrix on a dataset tile does not add up the tile's rows (matrix.go): a ratio or a distinct
// count cannot be summed. Each total is the dataset query again, grouped by fewer dimensions,
// and the levels are put together:
//
//	SELECT 0 AS hareno_level, b."region_name", b."category", b."margin_pct" FROM (<by region and category>) AS b
//	UNION ALL SELECT 1, b."region_name", NULL, b."margin_pct" FROM (<by region>) AS b
//	UNION ALL SELECT 3, NULL, NULL, b."margin_pct" FROM (<no grouping>) AS b
func (ds *Datasets) tile(ctx context.Context, connection string, conn Connection, q DatasetQuery, f Filters, dates *Dates, m *Matrix) (string, []string, bool, error) {
	base, err := ds.compile(ctx, connection, conn, q.Dataset, model.Query{Dimensions: q.Dimensions, Measures: q.Measures})
	if err != nil {
		return "", nil, false, err
	}
	on := f.on(base.text)
	conds := append(slices.Clone(q.Where), where(f, on)...)
	d := ds.ByName[q.Dataset]
	dated := dates != nil && d.Date != ""
	if dated {
		conds = append(conds, d.Between(dates.span(d.Today(time.Now()))))
	}
	if m == nil {
		if len(conds) == 0 {
			return base.sql, nil, false, nil
		}
		c, err := ds.compile(ctx, connection, conn, q.Dataset, model.Query{Dimensions: q.Dimensions, Measures: q.Measures, Where: conds})
		return c.sql, on, dated, err
	}
	var measures []string
	for _, v := range m.Values {
		measures = append(measures, v.Column)
	}
	dims := append(slices.Clone(m.Rows), m.Cols...)
	if slices.ContainsFunc(dims, func(d string) bool { return !slices.Contains(q.Dimensions, d) }) ||
		slices.ContainsFunc(measures, func(v string) bool { return !slices.Contains(q.Measures, v) }) {
		return "", nil, false, fmt.Errorf("%w: a matrix shows the tile's own fields and measures", ErrDataset)
	}
	var parts []string
	for depth := len(m.Rows); depth >= 0; depth-- {
		for _, across := range []bool{true, false} {
			if across && len(m.Cols) == 0 {
				continue
			}
			group := slices.Clone(m.Rows[:depth])
			if across {
				group = append(group, m.Cols...)
			}
			c, err := ds.compile(ctx, connection, conn, q.Dataset, model.Query{Dimensions: group, Measures: measures, Where: conds})
			if err != nil {
				return "", nil, false, err
			}
			level, sel := 0, []string{""}
			for i, d := range dims {
				if slices.Contains(group, d) {
					sel = append(sel, "b."+pgx.Identifier{d}.Sanitize())
				} else {
					level |= 1 << (len(dims) - 1 - i)
					sel = append(sel, "NULL")
				}
			}
			sel[0] = fmt.Sprint(level)
			if len(parts) == 0 {
				sel[0] += " AS hareno_level"
			}
			for _, v := range measures {
				sel = append(sel, "b."+pgx.Identifier{v}.Sanitize())
			}
			parts = append(parts, "SELECT "+strings.Join(sel, ", ")+" FROM (\n"+c.sql+"\n) AS b")
		}
	}
	return strings.Join(parts, "\nUNION ALL\n"), on, dated, nil
}

// tileQuery is the SQL a tile runs and its arguments, narrowed by the filters that apply to it
// (returned) and the date range (whether it applied is returned: dataset tiles only) and, with m,
// totalled as a matrix. cols are the columns of the tile's unfiltered result, which decide the
// filters on a SQL tile.
func (h *Handler) tileQuery(ctx context.Context, connection string, t Tile, cols []string, f Filters, dates *Dates, m *Matrix) (string, []any, []string, bool, error) {
	if t.Dataset != nil {
		if h.Datasets == nil {
			return "", nil, nil, false, fmt.Errorf("%w: datasets are not set up on this server", ErrDataset)
		}
		sql, on, dated, err := h.Datasets.tile(ctx, connection, h.Connections[connection], *t.Dataset, f, dates, m)
		return sql, nil, on, dated, err
	}
	sql, args := t.SQL, []any(nil)
	on := f.on(cols)
	if len(on) > 0 {
		sql, args = f.wrap(sql, on)
	}
	if m != nil {
		sql = m.wrap(sql)
	}
	return sql, args, on, false, nil
}

// pgTypes are the PostgreSQL types Malloy's result types are read back as.
var pgTypes = map[string]string{
	"string_type": "text", "number_type": "numeric", "boolean_type": "boolean",
	"date_type": "date", "timestamp_type": "timestamptz", "timestamptz_type": "timestamptz",
}

// timeFormats label a time bucket in the business time zone, as internal/query does.
var timeFormats = map[string]string{
	"year": "YYYY", "quarter": `YYYY-"Q"Q`, "month": "YYYY-MM", "week": "YYYY-MM-DD", "day": "YYYY-MM-DD",
	"hour": `YYYY-MM-DD"T"HH24:00`, "minute": `YYYY-MM-DD"T"HH24:MI`,
}

// unwrap turns compiled Malloy SQL, which returns each row as one JSON object, into SQL returning
// ordinary typed columns, ordered by the first dims columns:
//
//	SELECT hareno_r."region_name", to_char(hareno_r."order_month" AT TIME ZONE 'Asia/Hong_Kong', 'YYYY-MM') AS "order_month", ...
//	FROM (<Malloy SQL>) AS hareno_q(hareno_row) CROSS JOIN LATERAL json_to_record(hareno_row::json) AS hareno_r("region_name" text, ...)
//
// Numbers come back as numeric from their JSON text, so they keep their exact digits.
func unwrap(c compiler.Result, tz string, dims int) string {
	var sel, def, order []string
	for i, name := range c.Columns {
		id := pgx.Identifier{name}.Sanitize()
		kind, _ := c.Types[i]["kind"].(string)
		pg := pgTypes[kind]
		if pg == "" {
			pg = "text"
		}
		def = append(def, id+" "+pg)
		expr := "hareno_r." + id
		if frame, _ := c.Types[i]["timeframe"].(string); pg == "timestamptz" && timeFormats[frame] != "" {
			expr = fmt.Sprintf("to_char(%s AT TIME ZONE '%s', '%s')", expr, strings.ReplaceAll(tz, "'", "''"), timeFormats[frame])
		}
		sel = append(sel, expr+" AS "+id)
		if i < dims {
			order = append(order, fmt.Sprint(i+1))
		}
	}
	sql := "SELECT " + strings.Join(sel, ", ") + " FROM (\n" + strings.TrimRight(c.SQL, "; \t\r\n") +
		"\n) AS hareno_q(hareno_row) CROSS JOIN LATERAL json_to_record(hareno_row::json) AS hareno_r(" + strings.Join(def, ", ") + ")"
	if len(order) > 0 {
		sql += " ORDER BY " + strings.Join(order, ", ")
	}
	return sql
}

// ListDatasets serves GET /api/datasets: the datasets on connections where the user may build
// from datasets, with their dimensions and measures, sorted.
func (h *Handler) ListDatasets(w http.ResponseWriter, r *http.Request) {
	bearer, sub, ok := h.caller(w, r)
	if !ok {
		return
	}
	type dataset struct {
		Name       string                   `json:"name"`
		Connection string                   `json:"connection"`
		Dimensions []string                 `json:"dimensions"`
		Measures   []string                 `json:"measures"`
		Display    map[string]model.Display `json:"display"`
	}
	out := []dataset{}
	may := map[string]bool{}
	for _, d := range h.datasetList() {
		allowed, seen := may[d.Connection]
		if !seen {
			conn, ok := h.Connections[d.Connection]
			if !ok {
				continue
			}
			var err error
			allowed, err = h.mayDesign(r.Context(), bearer, sub, d.Connection, conn)
			if errors.Is(err, authz.ErrNoCompany) {
				allowed = false
			} else if err != nil {
				fail(w, http.StatusServiceUnavailable, "authorization unavailable")
				return
			}
			may[d.Connection] = allowed
		}
		if allowed {
			out = append(out, dataset{Name: d.Name, Connection: d.Connection, Dimensions: sortedNames(d.Dimensions), Measures: sortedNames(d.Measures),
				Display: shown(d, append(sortedNames(d.Dimensions), sortedNames(d.Measures)...))})
		}
	}
	reply(w, map[string]any{"datasets": out})
}

func (h *Handler) datasetList() []*model.Dataset {
	if h.Datasets == nil {
		return nil
	}
	var out []*model.Dataset
	for _, n := range sortedNames(h.Datasets.ByName) {
		out = append(out, h.Datasets.ByName[n])
	}
	return out
}

// mayDesign is whether the user may build tiles from datasets on the connection: dataset_user or sql_user.
func (h *Handler) mayDesign(ctx context.Context, bearer, sub, name string, c Connection) (bool, error) {
	company, err := h.Authz.Company(ctx, bearer, sub, c.Company)
	if err != nil {
		return false, err
	}
	design := authz.Item{Type: "connection", ID: name, Relation: "dataset_user"}
	use := authz.Item{Type: "connection", ID: name, Relation: "sql_user"}
	ok, err := h.Authz.Allowed(ctx, bearer, sub, company, []authz.Item{design, use})
	return ok[design] || ok[use], err
}

func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// display is how a dataset tile's columns are shown; nil for a SQL tile.
func (h *Handler) display(q *DatasetQuery) map[string]model.Display {
	if q == nil || h.Datasets == nil || h.Datasets.ByName[q.Dataset] == nil {
		return nil
	}
	return shown(h.Datasets.ByName[q.Dataset], append(slices.Clone(q.Dimensions), q.Measures...))
}

func shown(d *model.Dataset, names []string) map[string]model.Display {
	out := make(map[string]model.Display, len(names))
	for _, n := range names {
		out[n] = d.Shown(n)
	}
	return out
}
