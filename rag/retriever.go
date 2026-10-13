package rag

import (
	"context"
	"fmt"
	"rag-course/llm"
	"rag-course/vector"
)

const defaultTopK = 5

type Options struct {
	TopK     int
	Rewriter *Rewriter
}

// SearchResult is the public retrieval representation used by answer
// generation, citations, and evaluation. Keeping metadata structured avoids
// forcing downstream consumers to parse formatted prompt text.
type SearchResult struct {
	ChunkID    string
	DocumentID string
	Source     string
	Title      string
	Content    string
	Page       int
	Score      float64
	Method     string
}

type Retriever struct {
	embedder llm.Embedder
	store    vector.Store
	rewriter *Rewriter
	topK     int
}

func New(embedder llm.Embedder, store vector.Store, opts Options) *Retriever {
	topK := opts.TopK
	if topK <= 0 {
		topK = defaultTopK
	}
	return &Retriever{
		embedder: embedder,
		store:    store,
		rewriter: opts.Rewriter,
		topK:     topK,
	}
}

func (r *Retriever) Retrieve(ctx context.Context, history []llm.Message) (string, error) {
	hits, err := r.Search(ctx, history)
	if err != nil {
		return "", err
	}
	return formatSearchContext(hits), nil
}

// Search returns structured vector results independently of prompt assembly.
func (r *Retriever) Search(ctx context.Context, history []llm.Message) ([]SearchResult, error) {
	// build the query
	query := r.buildQuery(ctx, history)
	if query == "" {
		return nil, nil
	}

	// get the vectors
	vecs, err := r.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, nil
	}

	// get the hits
	hits, err := r.store.Query(ctx, vecs[0], r.topK)
	if err != nil {
		return nil, fmt.Errorf("vector query: %w", err)
	}
	if len(hits) == 0 {
		return nil, nil
	}
	results := make([]SearchResult, 0, len(hits))
	for _, h := range hits {
		results = append(results, SearchResult{
			ChunkID: h.ID, DocumentID: h.Metadata["document_id"], Source: h.Metadata["source"],
			Title: h.Metadata["title"], Content: h.Content, Page: pageNumber(h.Metadata),
			Score: float64(h.Score), Method: "vector",
		})
	}
	return results, nil
}

func pageNumber(metadata map[string]string) int {
	var n int
	_, _ = fmt.Sscanf(metadata["page"], "%d", &n)
	return n
}

func (r *Retriever) buildQuery(ctx context.Context, history []llm.Message) string {
	if r.rewriter != nil {
		if q, err := r.rewriter.Rewrite(ctx, history); err == nil && q != "" {
			return q
		}
	}
	return lastUserMessage(history)
}

func lastUserMessage(history []llm.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return history[i].Content
		}
	}
	return ""
}
