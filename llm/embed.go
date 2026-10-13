// This file adds embedding support to
// the llm package — a prerequisite for the ingest pipeline, which
// must turn chunk text into vectors before upserting them into the
// store.
package llm

import (
	"context"
	"fmt"

	"github.com/openai/openai-go/v3"
)

// Embedder turns text into dense vector representations
// suitable for similarity search. Implementations must be safe to call
// concurrently.
//
// What's an embedding, in 30 seconds:
//
//   - You hand the model a string.
//   - It hands you back an array of floats — typically 384, 768, 1536,
//     or 3072 numbers. That array is a point in a high-dimensional
//     vector space.
//   - Strings with similar MEANING land near each other in that space;
//     unrelated strings land far apart. "vampires hate garlic" and
//     "vampire weaknesses" will have a small cosine distance; "vampires
//     hate garlic" and "the price of tea in China" will have a large one.
//   - That spatial proximity is what makes retrieval work: embed the
//     user's question, find the chunks whose embeddings are closest to
//     it, hand those chunks to the chat model.
//
// Two practical constraints:
//
//  1. The dimension is fixed per model. text-embedding-3-small produces
//     1536 floats; nomic-embed-text produces 768. The vector store's
//     column type is locked to one number — switching models means
//     re-ingesting everything.
//  2. You must use the SAME embedding model for queries that you used
//     for ingest. Different models live in different vector spaces and
//     their coordinates are not interchangeable.
type Embedder interface {
	// Embed returns one vector per input string, in the same order. All
	// returned vectors share the same dimension. Implementations should
	// batch requests internally if the backend supports it.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Embed implements Embedder against an OpenAI-compatible
// /embeddings endpoint. The model is taken from Config.EmbeddingModel;
// the URL and credentials come from whichever constructor built this
// client (see New vs NewEmbedder in client.go).
//
// We always send the array form (OfArrayOfStrings) even when there is
// only one input; the server returns one embedding per input. Batching
// matters for ingest performance — one round trip embeds a whole
// document's worth of chunks instead of N.
//
// Two SDK quirks worth knowing:
//
//   - The API spec allows the server to return embeddings in arbitrary
//     order, so we index into the result by d.Index rather than trust
//     positional matching.
//   - The SDK decodes embeddings as []float64 (matching the JSON wire
//     format), but pgvector and the rest of this codebase work in
//     float32 to halve memory and bandwidth. We narrow at this boundary;
//     embedding values are well within float32 range so the cast is
//     lossless in practice.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

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
