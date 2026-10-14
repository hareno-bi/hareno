// Command loader feeds the dev mart new orders on a fixed interval, standing in for a client's
// load process. Each load inserts a few orders with lines and moves mart.etl_watermark in the
// same transaction, so the watermark never runs ahead of the data. Loaded orders have ids from
// 1,000,000; -clean removes them and restores the seed watermark (the known-answer checks in
// dev/mart/check.sh fail while they exist).
//
//	go run ./dev/mart/loader -every 100ms
//	go run ./dev/mart/loader -every 1m -orders 1 -keep 20   # the public demo: moving, but bounded
//	go run ./dev/mart/loader -clean
package main

import (
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
	keep := flag.Int("keep", 0, "keep only the latest this many loaded orders (0: keep all)")
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
		if err := load(ctx, conn, next, *orders, *keep); err != nil {
			if ctx.Err() != nil {
				continue // interrupted mid-load; the transaction rolled back
			}
			must(err)
		}
		next += *orders
		loads++
		if time.Since(began) > *every {
			late++ // the ticker drops ticks, so a slow mart loads less often rather than queueing
		}
	}
}

// load inserts n orders of 1 to 3 lines for the seed's customers and products, drops loaded
// orders beyond the latest keep (if keep > 0), and stamps the watermark, in one transaction.
func load(ctx context.Context, conn *pgx.Conn, id, n, keep int) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	b := &pgx.Batch{}
	for i := range n {
		b.Queue("INSERT INTO mart.fact_order VALUES ($1, $2, now(), 'complete', $3)", id+i, 1+rand.IntN(4), float64(rand.IntN(2000))/100)
		for line := range 1 + rand.IntN(3) {
			b.Queue(`INSERT INTO mart.fact_order_line SELECT $1, $2, product_id, $3, unit_cost * 2 + 1, NULL
				FROM mart.dim_product WHERE product_id = $4`, id+i, line+1, 1+rand.IntN(10), 1+rand.IntN(4))
		}
	}
	if keep > 0 {
		b.Queue("DELETE FROM mart.fact_order_line WHERE order_id >= $1 AND order_id < $2", firstID, id+n-keep)
		b.Queue("DELETE FROM mart.fact_order WHERE order_id >= $1 AND order_id < $2", firstID, id+n-keep)
	}
	b.Queue("UPDATE mart.etl_watermark SET loaded_at = clock_timestamp()")
	if err := tx.SendBatch(ctx, b).Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "loader:", err)
		os.Exit(1)
	}
}
