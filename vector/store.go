// This file defines the storage abstraction we'll use to talk to the database.
//
// Concrete backends (pgvector for now, possibly weaviate/qdrant/etc. later) live
// in subpackages so callers depend only on the interface.
//
// What is a vector store, conceptually?
//
// A vector store is a database that indexes high-dimensional float32
// vectors (the embeddings produced by an LLM embedder) and answers
// "give me the K rows whose vector is closest to this one" in
// sub-linear time. That nearest-neighbor search is the lookup half of
// RAG.
//
// You could implement Store with a flat in-memory slice for a few
// thousand chunks — just compute cosine distance against every row.
// That works and is sometimes the right answer for a course demo.
// The pgvector backend in this project uses an HNSW index instead, so
// the same code scales to millions of chunks without changing.
//
// Three things every backend has to handle:
//
//	dimension match  —  the query vector and stored vectors must be the
//	                    same length, set at ingest time.
//	distance metric  —  cosine, dot product, or Euclidean. We use cosine,
//	                    which is the right default for embedding models.
//	top-K ranking    —  return the closest K rows, plus a similarity
//	                    score the caller can filter or display.
package vector

import "context"

// Document is a single ingestible unit — typically a chunk of a larger
// source file. Embedding is populated by an llm.Embedder before the
// document reaches the store.
type Document struct {
	// ID is a stable identifier the store uses for upsert/delete. A
	// good default is "<source-path>#<chunk-index>".
	ID string

	// Content is the text that was embedded. Stored verbatim so it can
	// be returned to a RAG prompt assembler at query time.
	Content string

	// Metadata is arbitrary structured data associated with the chunk
	// (source filename, page number, ingest timestamp, ...). Backends
	// are expected to round-trip it without inspection.
	Metadata map[string]string

	// Embedding is the vector representation of Content. All documents
	// in a single store must share the same dimension.
	Embedding []float32
}

// Result is one hit from a similarity query.
type Result struct {
	Document

	// Score is the similarity between the query vector and the stored
	// vector. Higher is more similar; the exact metric (cosine,
	// inner-product, ...) depends on the backend's index configuration.
	//
	// Cosine similarity (what pgvector returns here) is in [-1, 1] for
	// arbitrary vectors but in [0, 1] for the normalized vectors that
	// almost every modern embedding model produces. A useful rule of
	// thumb for OpenAI/Nomic-style embeddings:
	//
	//	> 0.80   strongly related
	//	  0.60-0.80  related
	//	  0.40-0.60  weakly related
	//	  < 0.40   probably noise
	//
	// These thresholds are not universal; they shift with the
	// embedding model and the corpus.
	Score float32
}

// Store is the contract every vector backend implements. Methods take a
// context so callers can enforce timeouts and cancellation, which is
// especially important for ingest pipelines.
type Store interface {
	// Upsert inserts new documents or replaces existing ones by ID.
	// Implementations should perform this in a single transaction where
	// the backend supports it.
	Upsert(ctx context.Context, docs []Document) error

	// Query returns the topK documents most similar to the supplied
	// embedding. The embedding's dimension must match the store's
	// configured dimension; mismatches must surface as an error rather
	// than silent truncation.
	Query(ctx context.Context, embedding []float32, topK int) ([]Result, error)

	// Delete removes documents by ID. Missing IDs are not an error.
	Delete(ctx context.Context, ids []string) error

	// DeleteBySource removes every document whose "source" metadata
	// equals source. Used by the ingest pipeline to clear stale
	// chunks before re-upserting an edited file — without it, a file
	// re-ingested with fewer chunks than before would leave the
	// trailing chunks orphaned in the store.
	//
	// A source with no matching rows is not an error.
	DeleteBySource(ctx context.Context, source string) error

	// Close releases any underlying resources (DB pools, network
	// connections). Calling Close on an already-closed Store is a
	// no-op.
	Close() error
}
