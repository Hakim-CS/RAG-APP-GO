package config

import "testing"

func TestLoadAppliesSafeDefaults(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("EMBEDDING_MODEL", "")
	t.Setenv("EMBEDDING_DIM", "")
	c := Load()
	if c.HTTPAddr != "127.0.0.1:8080" {
		t.Fatalf("HTTPAddr = %q", c.HTTPAddr)
	}
	if c.EmbeddingDim != 768 {
		t.Fatalf("EmbeddingDim = %d", c.EmbeddingDim)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestValidateRejectsSameDirectories(t *testing.T) {
	c := Config{BaseURL: "http://localhost", Model: "model", EmbeddingDim: 3, EmbeddingModel: "embed", HTTPAddr: ":8080", IngestDir: "docs", ProcessedDir: "docs"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected same-directory validation error")
	}
}

