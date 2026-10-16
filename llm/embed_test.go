package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"godocs-rag/config"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// Embed must split large inputs into provider-sized requests and keep
// output order even when the server returns embeddings out of order.
func TestEmbedBatchesAndPreservesOrder(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var req struct{ Input []string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if len(req.Input) > embedBatchSize {
			t.Errorf("batch of %d exceeds %d", len(req.Input), embedBatchSize)
		}
		// Reply in reverse order; each vector encodes its input's text.
		var data []map[string]any
		for i := len(req.Input) - 1; i >= 0; i-- {
			var n float64
			fmt.Sscan(req.Input[i], &n)
			data = append(data, map[string]any{"object": "embedding", "index": i, "embedding": []float64{n}})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data, "model": "m"})
	}))
	defer srv.Close()

	c := NewEmbedder(config.Config{EmbeddingBaseURL: srv.URL, EmbeddingModel: "m"})
	texts := make([]string, embedBatchSize*2+1)
	for i := range texts {
		texts[i] = fmt.Sprint(i)
	}
	vecs, err := c.EmbedDocuments(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
	for i, v := range vecs {
		if int(v[0]) != i {
			t.Fatalf("vecs[%d] = %v, out of order", i, v)
		}
	}
}

// Queries and documents get their own configured prefix; stored text is
// never changed, only what is sent to the model.
func TestEmbedAddsTaskPrefixes(t *testing.T) {
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Input []string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		sent = append(sent, req.Input...)
		data := make([]map[string]any, len(req.Input))
		for i := range data {
			data[i] = map[string]any{"object": "embedding", "index": i, "embedding": []float64{0}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data, "model": "m"})
	}))
	defer srv.Close()

	c := NewEmbedder(config.Config{EmbeddingBaseURL: srv.URL, EmbeddingModel: "m",
		EmbeddingQueryPrefix: "q: ", EmbeddingDocumentPrefix: "d: "})
	if _, err := c.EmbedDocuments(context.Background(), []string{"doc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EmbedQuery(context.Background(), "question"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"d: doc", "q: question"}; !slices.Equal(sent, want) {
		t.Fatalf("sent %q, want %q", sent, want)
	}
}

// Stored vectors depend on the model and the document prefix, so both
// name the scheme; the query prefix doesn't change what is stored.
func TestEmbedderSchemeNamesModelAndDocumentPrefix(t *testing.T) {
	c := NewEmbedder(config.Config{EmbeddingModel: "m", EmbeddingQueryPrefix: "q: ", EmbeddingDocumentPrefix: "d: "})
	if got := c.Scheme(); got != "m|d: " {
		t.Fatalf("Scheme() = %q, want %q", got, "m|d: ")
	}
}
