// Package compilertest starts the real compile sidecar and connects to the dev mart, for
// integration tests. Tests using it run only when HARENO_INTEGRATION=1; they need the dev mart
// (docker compose up -d mart) and the sidecar's node_modules (npm ci in compiler/).
package compilertest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Root is the repository root.
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// Require skips the test unless integration tests are enabled.
func Require(t *testing.T) {
	t.Helper()
	if os.Getenv("HARENO_INTEGRATION") != "1" {
		t.Skip("set HARENO_INTEGRATION=1 to run against the dev mart and compile sidecar")
	}
}

// Mart connects to the dev mart as the restricted reader (HARENO_MART_DSN overrides).
func Mart(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HARENO_MART_DSN")
	if dsn == "" {
		dsn = "postgres://hareno_reader:hareno_reader@localhost:55433/mart"
	}
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

// MartModel returns the dev mart's Malloy model text.
func MartModel(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(Root(), "dev", "mart", "mart.malloy"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Sidecar starts compiler/server.ts on a free port and returns its URL; it stops with the test.
func Sidecar(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cmd := exec.Command("node", "server.ts")
	cmd.Dir = filepath.Join(Root(), "compiler")
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	for range 100 {
		if r, err := http.Get(url + "/healthz"); err == nil {
			r.Body.Close()
			return url
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("sidecar did not start")
	return ""
}
