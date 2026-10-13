// Package vector defines the storage contract for embedded chunks.
// Backends live in subpackages (pgvector) so callers depend only on
// the interface.
package vector

import "context"

// Document is one embedded chunk of a source file.
type Document struct {
	// ID is stable per chunk: "<source>#<chunk-index>".
	ID      string
	Content string
	// Metadata is round-tripped verbatim. Keys used today: source,
	// chunk_index, chunks, ingested_at, and for images type/image_path.
	Metadata  map[string]string
	Embedding []float32
}

// Result is one similarity-search hit. Score is cosine similarity;
// higher is more similar.
type Result struct {
	Document
	Score float32
}

type Store interface {
	// ReplaceSource atomically swaps every chunk whose "source"
	// metadata equals source for docs. Readers see either the old
	// chunks or the new ones, never neither. An empty docs deletes
	// the source.
	ReplaceSource(ctx context.Context, source string, docs []Document) error

	// Query returns the topK documents nearest to embedding.
	Query(ctx context.Context, embedding []float32, topK int) ([]Result, error)

	Close() error
}
