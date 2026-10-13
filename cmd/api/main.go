// Command api serves Hareno's HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rosschiu/kiban/sdk"
	"go.yaml.in/yaml/v3"

	"github.com/hareno-bi/hareno/internal/authz"
	"github.com/hareno-bi/hareno/internal/sqlapi"
	"github.com/hareno-bi/hareno/internal/sqlexec"
)

type config struct {
	Kiban struct {
		Gateway          string `yaml:"gateway"`
		ClientID         string `yaml:"client_id"`
		ClientSecretFile string `yaml:"client_secret_file"`
	} `yaml:"kiban"`
	Connections map[string]struct {
		Company string              `yaml:"company"`
		Mode    string              `yaml:"mode"`
		GateDSN string              `yaml:"gate_dsn"`
		SQLRole string              `yaml:"sql_role"`
		Timeout time.Duration       `yaml:"timeout"`
		MaxRows int                 `yaml:"max_rows"`
		Scope   map[string][]string `yaml:"scope"`
	} `yaml:"connections"`
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	cfgPath := flag.String("config", "", "configuration file (e.g. dev/hareno.yaml); without it only /healthz is served")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok\n"))
	})
	if *cfgPath != "" {
		h, err := sqlHandler(ctx, *cfgPath)
		if err != nil {
			log.Fatal(err)
		}
		mux.Handle("POST /api/sql", h)
	}
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	log.Printf("hareno api listening on %s", *addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func sqlHandler(ctx context.Context, path string) (*sqlapi.Handler, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg config
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	secret, err := os.ReadFile(cfg.Kiban.ClientSecretFile)
	if err != nil {
		return nil, fmt.Errorf("kiban client secret: %w", err)
	}
	k, err := sdk.New(ctx, sdk.Config{GatewayURL: cfg.Kiban.Gateway, ClientID: cfg.Kiban.ClientID, ClientSecret: strings.TrimSpace(string(secret))})
	if err != nil {
		return nil, err
	}
	h := &sqlapi.Handler{
		Authz:       &authz.Authz{K: k, Module: "hareno", Feature: "hareno.use", AllowTTL: 30 * time.Second, DenyTTL: 5 * time.Second},
		Connections: map[string]sqlapi.Connection{},
		Log:         slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("audit", "sql"),
	}
	for name, c := range cfg.Connections {
		if c.Mode != "database-enforced" {
			return nil, fmt.Errorf("connection %s: the SQL editor needs mode database-enforced, not %q", name, c.Mode)
		}
		db, err := pgxpool.New(ctx, c.GateDSN)
		if err != nil {
			return nil, fmt.Errorf("connection %s: %w", name, err)
		}
		h.Connections[name] = sqlapi.Connection{
			Company: c.Company,
			Exec:    sqlexec.Exec{DB: db, Role: c.SQLRole, Timeout: c.Timeout, MaxRows: c.MaxRows},
			Scope:   c.Scope,
		}
	}
	return h, nil
}
