package app

// Package app wires the pieces of lesson 1 together: the LLM client
// and the chat REPL. Keeping orchestration here means cmd/rag/main.go
// stays a thin shim that's easy to read.
//
// The shape of the program in lesson 1, viewed from this file:
//
//	llm.Client      talks HTTP to the chat-completions endpoint
//	chat.RunREPL    foreground: terminal chat loop
//
// Later lessons will grow this file: a vector store will be opened
// here, a background ingest watcher will be spawned, an optional web
// server will start alongside the REPL. The wiring point is always
// the same — Run takes a Config, builds the components, and starts
// the foreground loop.
import (
	"context"
	"log"
	"os"
	"rag-course/chat"
	"rag-course/config"
	"rag-course/ingest"
	"rag-course/llm"
	"rag-course/rag"
	"rag-course/vector"
	"rag-course/vector/pgvector"
	"rag-course/web"
	"sync"
)

// Run is the program's main loop. In lesson 1 there is only the
// foreground REPL, so Run constructs the LLM client and hands it
// straight to chat.RunREPL.
func Run(parent context.Context, cfg config.Config) error {
	// A stderr-tagged logger so connection-related
	// status lines ("vector store ready", "vector store disabled: ...")
	// are clearly distinguishable from chat output on stdout.
	logger := log.New(os.Stderr, "[rag] ", log.LstdFlags)

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	client := llm.New(cfg)

	embedder := llm.NewEmbedder(cfg)

	// Open the vector store. A nil store with a
	// nil error means "no DATABASE_URL configured" — the chat path
	// works fine without a database, so we surface the reason and
	// keep going. Any real error (bad DSN, server unreachable,
	// migration failure) is also logged but not fatal.
	store, err := openStore(ctx, cfg)
	if err != nil {
		logger.Printf("vector store disabled: %v", err)
	}

	var wg sync.WaitGroup
	if store != nil {
		wg.Go(func() {
			opts := ingest.Options{
				SourceDir:    cfg.IngestDir,
				ProcessedDir: cfg.ProcessedDir,
			}

			if err := ingest.Watch(ctx, opts, embedder, store, logger); err != nil && ctx.Err() == nil {
				logger.Printf("watcher stopped: %v", err)
			}
		})
		logger.Printf("watching %s for new documents", cfg.IngestDir)
	}

	// Defer Close so the connection pool drains
	// cleanly on exit (Ctrl-C, REPL quit, or any error). Guarded by
	// the nil-check because openStore returns a nil interface when
	// DATABASE_URL is unset.
	if store != nil {
		defer store.Close()
		logger.Printf("vector store ready")
	}

	// get a retriever, which means we need a rewriter
	var retriever *rag.Retriever
	if store != nil {
		retriever = rag.New(embedder, store, rag.Options{
			TopK:     5,
			Rewriter: rag.NewRewriter(client),
		})
	}

	if cfg.HTTPAddr != "" {
		srv, err := web.New(client, embedder, retriever, web.Options{
			Addr:             cfg.HTTPAddr,
			SystemPromptFile: cfg.SystemPromptFile,
			Store:            store,
			ProcessedDir:     cfg.ProcessedDir,
			ImagesDir:        cfg.ImageDir,
			Ready:            store != nil,
		})

		if err != nil {
			logger.Printf("web server disabled: %v", err)
		} else {
			wg.Go(func() {
				if err := srv.Run(ctx, cfg.HTTPAddr); err != nil && ctx.Err() == nil {
					logger.Printf("web server stopped: %v", err)
				}
			})
			logger.Printf("web chat at http://localhost%s/chat", cfg.HTTPAddr)
		}
	}

	replErr := chat.RunREPL(ctx, client, retriever, chat.Options{
		SystemPromptFile: cfg.SystemPromptFile,
	})

	cancel()
	wg.Wait()
	return replErr
}

// openStore returns a configured vector.Store, or
// (nil, nil) when no DATABASE_URL is set. The (nil, nil) case is
// intentional and signals "feature disabled, not an error" to the
// caller.
//
// Note: we explicitly drop the concrete *pgvector.Store on error and
// return a nil interface. Returning the typed nil directly would box a
// nil pointer into a non-nil vector.Store interface, defeating the
// "if store != nil" check in Run.
func openStore(ctx context.Context, cfg config.Config) (vector.Store, error) {
	if cfg.DatabaseURL == "" {
		return nil, nil
	}
	s, err := pgvector.New(ctx, pgvector.Options{
		DSN:          cfg.DatabaseURL,
		EmbeddingDim: cfg.EmbeddingDim,
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}
