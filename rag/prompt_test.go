package rag

import (
	"godocs-rag/llm"
	"strings"
	"testing"
)

// The system prompt renders images from the [image: <path>] tag, so the
// formatter must emit it for image hits.
func TestFormatContextTagsImages(t *testing.T) {
	got := formatContext([]SearchResult{
		{Source: "gopher.png", ImagePath: "/images/1-gopher.png", Content: "a gopher", Score: 0.8},
		{Source: "sync.txt", Content: "WaitGroup", Score: 0.7},
	})
	if !strings.Contains(got, "[1] Source: gopher.png [image: /images/1-gopher.png]") {
		t.Fatalf("image tag missing:\n%s", got)
	}
	if strings.Count(got, "[image:") != 1 {
		t.Fatalf("text hit was tagged as image:\n%s", got)
	}
}

func TestWithContextLeavesHistoryUntouched(t *testing.T) {
	history := []llm.Message{{Role: "user", Content: "q"}}
	out := WithContext(history, "ctx")
	if history[0].Content != "q" || !strings.HasPrefix(out[0].Content, "ctx") {
		t.Fatalf("history=%q out=%q", history[0].Content, out[0].Content)
	}
}
