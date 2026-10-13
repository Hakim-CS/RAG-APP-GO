// Command rag runs the Go docs assistant: terminal chat, web chat,
// and a watcher that ingests files dropped into INGEST_DIR.
package main

import (
	"context"
	"fmt"
	"godocs-rag/app"
	"godocs-rag/config"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Ctrl-C / SIGTERM cancels every in-flight model request, the
	// watcher and the web server via this one context.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid configuration: %v\n", err)
		os.Exit(2)
	}
	if err := app.Run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
