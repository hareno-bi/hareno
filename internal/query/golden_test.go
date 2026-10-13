package query

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/hareno-bi/hareno/internal/compiler"
	"github.com/hareno-bi/hareno/internal/compiler/compilertest"
)

type golden struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	Gap   string `yaml:"gap"`
	Steps []struct {
		Q      string      `yaml:"q"`
		Reject bool        `yaml:"reject"`
		Expect [][]*string `yaml:"expect"`
	} `yaml:"steps"`
}

// TestGolden runs the S1 golden queries through compile, execute and decode, against the dev
// mart's known answers. Cases marked `gap` must still fail until the gap is closed (P2-03).
func TestGolden(t *testing.T) {
	compilertest.Require(t)
	b, err := os.ReadFile("testdata/golden.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cases []golden
	if err := yaml.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	r := martRunner(t)
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			problem := run(r, c)
			switch {
			case c.Gap == "" && problem != "":
				t.Errorf("%s: %s", c.Title, problem)
			case c.Gap != "" && problem == "":
				t.Errorf("%s now passes: remove its gap marker (%s)", c.ID, c.Gap)
			case c.Gap != "":
				t.Logf("known gap, still failing as expected: %s\n  %s", c.Gap, problem)
			}
		})
	}
}

// run returns "" when every step behaves as expected, otherwise what went wrong.
func run(r Runner, c golden) string {
	for _, s := range c.Steps {
		res, err := r.Run(context.Background(), s.Q)
		if s.Reject {
			if err == nil {
				return fmt.Sprintf("must be rejected, but returned %v", res.Rows)
			}
			continue
		}
		if err != nil {
			return err.Error()
		}
		if !rowsMatch(res.Rows, s.Expect) {
			return fmt.Sprintf("expected %s\n  actual   %v", show(s.Expect), res.Rows)
		}
	}
	return ""
}

func martRunner(t *testing.T) Runner {
	return Runner{
		Compiler: &compiler.Client{URL: compilertest.Sidecar(t)},
		DB:       compilertest.Mart(t),
		Model: Model{URL: "file://hareno/mart.malloy", Files: map[string]string{"file://hareno/mart.malloy": compilertest.MartModel(t)},
			Connection: "mart", Dialect: "postgres"},
	}
}

func rowsMatch(actual [][]Value, expected [][]*string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if len(actual[i]) != len(expected[i]) {
			return false
		}
		for j := range actual[i] {
			if !same(actual[i][j], expected[i][j]) {
				return false
			}
		}
	}
	return true
}

var (
	monthOrDate = regexp.MustCompile(`^\d{4}-\d{2}(-\d{2})?$`)
	businessTZ  = mustZone("Asia/Hong_Kong") // dev mart test convention
)

// same compares one value. Numbers: the actual value, rounded half-even to the expected number of
// decimals, equals the expected value exactly. Months and dates: timestamps are rendered in
// business time first, as Hareno will display them.
func same(actual Value, expected *string) bool {
	if expected == nil || actual == nil {
		return expected == nil && actual == nil
	}
	want := *expected
	if exp, ok := new(big.Rat).SetString(want); ok {
		got, ok := actual.(json.Number)
		if !ok {
			return false
		}
		g, ok := new(big.Rat).SetString(string(got))
		if !ok {
			return false
		}
		decimals := 0
		if i := strings.IndexByte(want, '.'); i >= 0 {
			decimals = len(want) - i - 1
		}
		return roundHalfEven(g, decimals).Cmp(exp) == 0
	}
	text := fmt.Sprint(actual)
	if monthOrDate.MatchString(want) {
		if ts, err := time.Parse(time.RFC3339, text); err == nil {
			text = ts.In(businessTZ).Format(time.RFC3339)
		}
		return strings.HasPrefix(text, want)
	}
	return text == want
}

func roundHalfEven(x *big.Rat, decimals int) *big.Rat {
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	v := new(big.Rat).Mul(x, scale)
	q, rem := new(big.Int).QuoRem(v.Num(), v.Denom(), new(big.Int)) // truncates toward zero
	twice := new(big.Int).Mul(new(big.Int).Abs(rem), big.NewInt(2))
	if c := twice.Cmp(v.Denom()); c > 0 || (c == 0 && q.Bit(0) == 1) {
		q.Add(q, big.NewInt(int64(v.Sign())))
	}
	return new(big.Rat).Quo(new(big.Rat).SetInt(q), scale)
}

func show(rows [][]*string) string {
	var out []string
	for _, r := range rows {
		var vals []string
		for _, v := range r {
			if v == nil {
				vals = append(vals, "null")
			} else {
				vals = append(vals, *v)
			}
		}
		out = append(out, "["+strings.Join(vals, " ")+"]")
	}
	return "[" + strings.Join(out, " ") + "]"
}

func mustZone(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func TestSame(t *testing.T) {
	s := func(v string) *string { return &v }
	for _, tc := range []struct {
		actual Value
		want   *string
		ok     bool
	}{
		{json.Number("238.5000"), s("238.50"), true},
		{json.Number("83.96428571"), s("83.9643"), true},
		{json.Number("0.125"), s("0.12"), true}, // half-even
		{json.Number("0.135"), s("0.14"), true}, // half-even
		{json.Number("1234567890123456.8"), s("1234567890123456.78"), false},
		{json.Number("0"), nil, false},
		{nil, nil, true},
		{"2025-12-31T16:00:00+00:00", s("2026-01"), true}, // UTC instant of HKT January
		{"North", s("North"), true},
	} {
		if got := same(tc.actual, tc.want); got != tc.ok {
			t.Errorf("same(%v, %v) = %v, want %v", tc.actual, show([][]*string{{tc.want}}), got, tc.ok)
		}
	}
}
