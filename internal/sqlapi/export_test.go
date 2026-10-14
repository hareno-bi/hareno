package sqlapi

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCell(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"North", "North"},
		{"=HYPERLINK(\"http://x\")", "'=HYPERLINK(\"http://x\")"},
		{"+1", "'+1"}, {"-1", "'-1"}, {"@SUM(A1)", "'@SUM(A1)"}, {"\tx", "'\tx"},
		{json.Number("-1234567890123456.78"), "-1234567890123456.78"}, // numbers are not formulas
		{-1.5, "-1.5"}, {1e21, "1000000000000000000000"}, {int64(-3), "-3"}, {true, "true"},
		{time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), "2026-10-14"},
		{time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC), "2026-10-14 09:30:00"},
		{map[string]any{"a": "=1"}, `{"a":"=1"}`},
		{[]any{"=1"}, `["=1"]`},
	} {
		if got := cell(tc.in); got != tc.want {
			t.Errorf("cell(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
