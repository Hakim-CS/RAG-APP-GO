package ingest

import (
	"strings"
	"testing"
)

func TestChunkProducesOverlap(t *testing.T) {
	text := "one two three four five six seven eight nine ten"
	chunks := chunk(text, 20, 5)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks", len(chunks))
	}
	if !strings.Contains(chunks[0], "four") || !strings.Contains(chunks[1], "four") {
		t.Fatalf("expected boundary word to be preserved across chunks: %q / %q", chunks[0], chunks[1])
	}
}

func TestChunkRejectsEmptyInput(t *testing.T) {
	if got := chunk("  ", 20, 5); got != nil {
		t.Fatalf("got %#v", got)
	}
}
