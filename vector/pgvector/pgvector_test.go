package pgvector

import (
	"context"
	"godocs-rag/vector"
	"os"
	"testing"
)

const testDim = 768

// TEST_DATABASE_URL must point at a disposable database (CI uses the
// eval service). The test only touches rows of its own source.
func openTest(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	s, err := New(context.Background(), Options{DSN: dsn, EmbeddingDim: testDim})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func doc(id, source string, dim int) vector.Document {
	v := make([]float32, dim)
	v[0] = 1
	return vector.Document{ID: id, Content: id, Metadata: map[string]string{"source": source}, Embedding: v}
}

func countSource(t *testing.T, s *Store, source string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM documents WHERE metadata->>'source' = $1`, source).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A failed re-ingest must leave the previous version in place.
func TestReplaceSourceIsAtomic(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	const src = "__test_replace__.txt"
	t.Cleanup(func() { s.ReplaceSource(ctx, src, nil) })

	if err := s.ReplaceSource(ctx, src, []vector.Document{doc(src+"#0", src, testDim), doc(src+"#1", src, testDim)}); err != nil {
		t.Fatal(err)
	}

	// The second document has the wrong dimension, so the insert fails
	// after the delete has already run inside the transaction.
	err := s.ReplaceSource(ctx, src, []vector.Document{doc(src+"#0", src, testDim), doc(src+"#1", src, 3)})
	if err == nil {
		t.Fatal("expected dimension error")
	}
	if n := countSource(t, s, src); n != 2 {
		t.Fatalf("after failed replace: %d chunks, want the original 2", n)
	}

	if err := s.ReplaceSource(ctx, src, []vector.Document{doc(src+"#0", src, testDim)}); err != nil {
		t.Fatal(err)
	}
	if n := countSource(t, s, src); n != 1 {
		t.Fatalf("after replace: %d chunks, want 1", n)
	}
}

// Rows are counted by the scheme they were embedded with; rows from before
// schemes were recorded count under "".
func TestEmbedSchemesCountsRows(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	const src = "__test_scheme__.txt"
	t.Cleanup(func() { s.ReplaceSource(ctx, src, nil) })
	d0, d1 := doc(src+"#0", src, testDim), doc(src+"#1", src, testDim)
	d0.Metadata["embed_scheme"] = "__scheme__"
	d1.Metadata["embed_scheme"] = "__scheme__"
	if err := s.ReplaceSource(ctx, src, []vector.Document{d0, d1}); err != nil {
		t.Fatal(err)
	}
	counts, err := s.embedSchemes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts["__scheme__"] != 2 {
		t.Fatalf("counts = %v, want 2 rows for __scheme__", counts)
	}
}

// A query embedded one way against documents embedded another still
// returns results, just worse ones, so a mismatch has to be reported.
func TestSchemeErrRejectsMixedIndex(t *testing.T) {
	for _, tc := range []struct {
		counts map[string]int
		ok     bool
	}{
		{map[string]int{"a": 3}, true},
		{map[string]int{}, true}, // empty index: nothing to mismatch
		{map[string]int{"a": 3, "": 1}, false},
		{map[string]int{"b": 3}, false},
	} {
		if err := schemeErr(tc.counts, "a"); (err == nil) != tc.ok {
			t.Errorf("schemeErr(%v, a) = %v, want ok=%v", tc.counts, err, tc.ok)
		}
	}
}
