// Command loader feeds the dev mart new orders on a fixed interval, standing in for a client's
// load process. Each load inserts a few orders with lines and moves mart.etl_watermark in the
// same transaction, so the watermark never runs ahead of the data. Loaded orders have ids from
// 1,000,000; -clean removes them and restores the seed watermark (the known-answer checks in
// dev/mart/check.sh fail while they exist).
//
// With -cycle N, the mart moves without growing: each load adds one order of one line, N times,
// then removes the oldest loaded order, N times, and repeats. -every 5s -cycle 6 is one minute.
//
//	go run ./dev/mart/loader -every 100ms
//	go run ./dev/mart/loader -every 5s -cycle 6   # the public demo: moves every 5 s, never grows
//	go run ./dev/mart/loader -clean
package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"time"

	"github.com/jackc/pgx/v5"
)

const firstID = 1_000_000

func main() {
	dsn := flag.String("dsn", "postgres://postgres:dev@localhost:55433/mart", "mart admin connection (dev-only)")
	every := flag.Duration("every", 100*time.Millisecond, "load interval")
	orders := flag.Int("orders", 5, "orders per load")
	clean := flag.Bool("clean", false, "remove loaded orders, restore the seed watermark, and exit")
	cycle := flag.Int("cycle", 0, "add one single-line order per load this many times, then remove one as many times, and repeat (0: only add)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	conn, err := pgx.Connect(ctx, *dsn)
	must(err)
	defer conn.Close(context.Background())

	if *clean {
		_, err := conn.Exec(ctx, fmt.Sprintf(`
			DELETE FROM mart.fact_order_line WHERE order_id >= %[1]d;
			DELETE FROM mart.fact_order WHERE order_id >= %[1]d;
			UPDATE mart.etl_watermark SET loaded_at = '2026-10-12 06:00+00';`, firstID))
		must(err)
		return
	}

	if *cycle > 0 { // start the cycle from the seed
		_, err := conn.Exec(ctx, fmt.Sprintf(`DELETE FROM mart.fact_order_line WHERE order_id >= %[1]d; DELETE FROM mart.fact_order WHERE order_id >= %[1]d;`, firstID))
		must(err)
	}
	var next int
	must(conn.QueryRow(ctx, "SELECT greatest(coalesce(max(order_id) + 1, 0), $1) FROM mart.fact_order", firstID).Scan(&next))
	start, loads, late := time.Now(), 0, 0
	t := time.NewTicker(*every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "loader: %d loads in %.1fs (%d over the interval)\n", loads, time.Since(start).Seconds(), late)
			return
		case <-t.C:
		}
		began := time.Now()
		var err error
		switch {
		case *cycle == 0:
			err = load(ctx, conn, next, *orders, 0)
			next += *orders
		case loads%(2**cycle) < *cycle:
			err = load(ctx, conn, next, 1, 1)
			next++
		default:
			err = unload(ctx, conn)
		}
		if err != nil {
			if ctx.Err() != nil {
				continue // interrupted mid-load; the transaction rolled back
			}
			must(err)
		}
		loads++
		if time.Since(began) > *every {
			late++ // the ticker drops ticks, so a slow mart loads less often rather than queueing
		}
	}
}

// load inserts n orders of lines lines each (1 to 3 at random when 0) for the seed's customers
// and products, and stamps the watermark, in one transaction.
func load(ctx context.Context, conn *pgx.Conn, id, n, lines int) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	b := &pgx.Batch{}
	for i := range n {
		b.Queue("INSERT INTO mart.fact_order VALUES ($1, $2, now(), 'complete', $3)", id+i, 1+rand.IntN(4), float64(rand.IntN(2000))/100)
		for line := range cmp.Or(lines, 1+rand.IntN(3)) {
			b.Queue(`INSERT INTO mart.fact_order_line SELECT $1, $2, product_id, $3, unit_cost * 2 + 1, NULL
				FROM mart.dim_product WHERE product_id = $4`, id+i, line+1, 1+rand.IntN(10), 1+rand.IntN(4))
		}
	}
	b.Queue("UPDATE mart.etl_watermark SET loaded_at = clock_timestamp()")
	if err := tx.SendBatch(ctx, b).Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// unload removes the oldest loaded order and stamps the watermark, in one transaction.
func unload(ctx context.Context, conn *pgx.Conn) error {
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			WITH oldest AS (SELECT min(order_id) AS id FROM mart.fact_order WHERE order_id >= $1),
			     lines AS (DELETE FROM mart.fact_order_line WHERE order_id = (SELECT id FROM oldest))
			DELETE FROM mart.fact_order WHERE order_id = (SELECT id FROM oldest)`, firstID)
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE mart.etl_watermark SET loaded_at = clock_timestamp()")
		}
		return err
	})
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "loader:", err)
		os.Exit(1)
	}
}
