package compiler

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres answers schema requests from a PostgreSQL source, using the restricted reader
// credential: it only runs zero-row SELECTs.
type Postgres struct{ DB *pgxpool.Pool }

func (p Postgres) TableSchema(ctx context.Context, table string) ([]Field, error) {
	return p.SQLSchema(ctx, "SELECT * FROM "+pgx.Identifier(strings.Split(table, ".")).Sanitize())
}

func (p Postgres) SQLSchema(ctx context.Context, sql string) ([]Field, error) {
	rows, err := p.DB.Query(ctx, "SELECT * FROM ("+sql+") AS t LIMIT 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var fields []Field
	for _, d := range rows.FieldDescriptions() {
		t, ok := pgTypes[d.DataTypeOID]
		if !ok {
			return nil, fmt.Errorf("column %s: unsupported PostgreSQL type oid %d", d.Name, d.DataTypeOID)
		}
		fields = append(fields, Field{Kind: "dimension", Name: d.Name, Type: t})
	}
	return fields, rows.Err()
}

// pgTypes maps PostgreSQL type OIDs to Malloy atomic types. Unlisted types are refused rather than
// guessed.
var pgTypes = map[uint32]map[string]any{
	16:   {"kind": "boolean_type"},
	20:   {"kind": "number_type", "subtype": "bigint"},
	21:   {"kind": "number_type", "subtype": "integer"},
	23:   {"kind": "number_type", "subtype": "integer"},
	700:  {"kind": "number_type", "subtype": "decimal"},
	701:  {"kind": "number_type", "subtype": "decimal"},
	1700: {"kind": "number_type", "subtype": "decimal"},
	25:   {"kind": "string_type"},
	1043: {"kind": "string_type"},
	1082: {"kind": "date_type"},
	1114: {"kind": "timestamp_type"},
	1184: {"kind": "timestamptz_type"},
}
