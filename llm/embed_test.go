package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"godocs-rag/config"
	"net/http"
	"net/http/httptest"
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
	vecs, err := c.Embed(context.Background(), texts)
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
