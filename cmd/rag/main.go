package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"rag-course/app"
	"rag-course/config"
	"syscall"
)

func main() {
	// We need to:
	// - set up the app
	// - set up config
	// - set up an LLM client
	// - set up the Read-Eval-Print loop (REPL)

	// Cancel everything cleanly on Ctrl-C / SIGTERM. The same context
	// reaches the REPL and the HTTP request to the chat endpoint, so
	// pressing Ctrl-C while the model is replying tears the request
	// down instead of leaving it dangling.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Calling app.Run will actually call chat.RunREPL, which runs the Read-Eval-Print Loop for our llm chat.
	// Later, it will also run the web based version.
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
