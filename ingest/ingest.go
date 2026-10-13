// Package ingest takes documents
// from a source directory, chunks them, embeds the chunks, upserts the
// result into a vector.Store, and then moves the originals into a
// "processed" directory. It exposes a long-running Watch entry point
// intended to run as a background goroutine alongside the chat REPL.
//
// The pipeline, in five steps, is the heart of the "indexing side" of
// RAG:
//
//  1. READ   — open a supported text/markdown file from disk.
//  2. CHUNK  — split it into ~1000-byte overlapping windows. We chunk
//     because (a) embedding models have a max input length,
//     (b) we want retrieval to return the *relevant passage*,
//     not a whole document, and (c) shorter chunks produce
//     embeddings that capture local meaning more sharply.
//  3. EMBED  — call the embeddings model once with the full batch of
//     chunks. Each chunk becomes a fixed-dimension float32
//     vector. See internal/llm/embed.go for what an
//     "embedding" actually is.
//  4. DELETE — remove any prior chunks that share this filename, so
//     re-ingesting an edited file leaves no orphans behind.
//  5. UPSERT — insert (or replace) the new chunks in the vector store
//     with the embeddings attached.
//
// At query time (in a later lesson) the user's question will be
// embedded with the SAME model and the store returns the chunks whose
// embeddings sit closest to it. Steps 3 and 5 here are what make that
// lookup possible.

package ingest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"rag-course/llm"
	"rag-course/vector"
	"strconv"
	"strings"
	"time"
)

// Default chunking parameters. Both numbers are bytes.
//
//	ChunkSize=1000     A sweet spot for prose: large enough to carry a
//	                   self-contained idea (a paragraph or two), small
//	                   enough that the embedding doesn't average too
//	                   many distinct topics into one vector.
//	ChunkOverlap=100   The first 100 bytes of chunk N+1 repeat the last
//	                   100 bytes of chunk N. Overlap matters when an
//	                   important sentence happens to straddle a chunk
//	                   boundary — without overlap, the model would see
//	                   half of it on each side and might miss the
//	                   point. With overlap, the full sentence appears
//	                   intact in at least one chunk.
const (
	defaultChunkSize    = 1000
	defaultChunkOverlap = 100
)

// Options configures an ingest run.
type Options struct {
	// SourceDir is watched for new files (non-recursively).
	SourceDir string

	// ProcessedDir receives originals after a successful upsert. It
	// must differ from SourceDir; otherwise moved files would be
	// re-detected and re-ingested in a loop.
	ProcessedDir string

	// ChunkSize and ChunkOverlap control text splitting (in bytes).
	// Sensible defaults are applied when zero.
	ChunkSize    int
	ChunkOverlap int
}

// processOne runs the per-file pipeline: read the file,
// then hand the bytes off to processContent which does the actual
// chunk → embed → upsert work.
func processOne(ctx context.Context, path string, opts Options, embedder llm.Embedder, store vector.Store) error {
	if !supportedFormat(path) {
		return fmt.Errorf("unsupported format: %s", filepath.Ext(path))
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	_, err = ProcessContent(ctx, filepath.Base(path), raw, opts, embedder, store)
	return err
}

// ProcessContent ingests an in-memory document under the
// given source name and returns the number of chunks produced. The
// source name is used as the chunk-id prefix and the "source" metadata
// field; only its basename is significant.
//
// Stable ID scheme: "<basename>#<chunk-index>". The delete-then-upsert
// flow ensures re-ingesting an edited file leaves no orphaned chunks
// even when the new content produces fewer chunks than the old.
//
// Note: delete and upsert are NOT in a single transaction. There is a
// brief window where the file has zero chunks in the store; a
// retrieval that lands in that window will simply return fewer hits.
// For a course-pace pipeline this is invisible.
func ProcessContent(ctx context.Context, source string, content []byte, opts Options, embedder llm.Embedder, store vector.Store) (int, error) {
	if embedder == nil {
		return 0, errors.New("embedder is required")
	}

	if store == nil {
		return 0, errors.New("vector store is required")
	}

	base := filepath.Base(source)
	if !supportedFormat(base) {
		return 0, fmt.Errorf("unsupported format: %s", filepath.Ext(base))
	}

	size := opts.ChunkSize
	if size <= 0 {
		size = defaultChunkSize
	}

	overlap := opts.ChunkOverlap
	if overlap <= 0 {
		overlap = defaultChunkOverlap
	}

	text := strings.TrimSpace(string(content))
	if text == "" {
		return 0, errors.New("file is empty")
	}

	chunks := chunk(text, size, overlap)
	if len(chunks) == 0 {
		return 0, errors.New("no chunks produced")
	}

	vectors, err := embedder.Embed(ctx, chunks)
	if err != nil {
		return 0, fmt.Errorf("embed: %w", err)
	}

	if len(vectors) != len(chunks) {
		return 0, fmt.Errorf("embed: got %d vectors for %d chunks", len(vectors), len(chunks))
	}

	if err := store.DeleteBySource(ctx, base); err != nil {
		return 0, fmt.Errorf("clear previous chunks: %w", err)
	}

	ingestedAt := time.Now().UTC().Format(time.RFC3339)
	docs := make([]vector.Document, len(chunks))
	for i, c := range chunks {
		docs[i] = vector.Document{
			ID:      fmt.Sprintf("%s#%d", base, i),
			Content: c,
			Metadata: map[string]string{
				"source":      base,
				"chunk_index": strconv.Itoa(i),
				"chunks":      strconv.Itoa(len(chunks)),
				"ingested_at": ingestedAt,
			},
			Embedding: vectors[i],
		}
	}

	if err := store.Upsert(ctx, docs); err != nil {
		return 0, err
	}

	return len(chunks), nil
}

func IsSupported(name string) bool {
	return supportedFormat(name)
}

// supportedFormat reports whether path's extension is one
// this package knows how to read. Plain text and Markdown are treated
// identically; PDF and others can be added with format-specific
// readers later.
func supportedFormat(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".markdown":
		return true
	}
	return false
}
