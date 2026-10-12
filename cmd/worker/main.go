// Command worker runs Hareno's background jobs (River consumers, ARCHITECTURE-PLAN.md §18.6).
// No job kinds exist yet; it starts and waits for a shutdown signal.
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Print("hareno worker started")
	<-ctx.Done()
	log.Print("hareno worker stopped")
}
