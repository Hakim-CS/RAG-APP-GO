package llm

import (
	"context"
	"fmt"

	"github.com/openai/openai-go/v3"
)

// Embedder turns texts into vectors, one per input, in order. Queries
// and documents must use the same model: vectors from different models
// are not comparable. Implementations must be safe for concurrent use.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// embedBatchSize caps inputs per request. Providers limit request size
// (OpenAI: 2048 inputs and a token budget), so a large document is sent
// in several requests rather than one that gets rejected.
const embedBatchSize = 64

// Embed implements Embedder against an OpenAI-compatible /embeddings
// endpoint using Config.EmbeddingModel.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += embedBatchSize {
		batch := texts[start:min(start+embedBatchSize, len(texts))]
		vecs, err := c.embedBatch(ctx, batch)
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (c *Client) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	resp, err := c.sdk.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Model: c.cfg.EmbeddingModel,
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings: expected %d vectors; got %d", len(texts), len(resp.Data))
	}

	// The API may return embeddings out of order; place them by Index.
	// The SDK decodes float64; pgvector stores float32.
	vecs := make([][]float32, len(texts))
	for _, d := range resp.Data {
		idx := int(d.Index)
		if idx < 0 || idx >= len(vecs) {
			return nil, fmt.Errorf("embeddings: index %d out of range", idx)
		}
		vec := make([]float32, len(d.Embedding))
		for i, f := range d.Embedding {
			vec[i] = float32(f)
		}
		vecs[idx] = vec
	}
	return vecs, nil
}
