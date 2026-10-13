// A boundary-aware byte splitter used by the ingest pipeline.
package ingest

import "strings"

// chunk splits text into overlapping windows of approximately
// size bytes. When the window's right edge falls near a paragraph,
// sentence, or word boundary, the cut is moved to that boundary so
// chunks read more naturally and similarity search aligns with
// human-meaningful units.
//
// Why boundary-aware chunking matters: an embedding is a single vector
// summarizing the meaning of its input. If you slice in the middle of
// a sentence, both halves get a degraded embedding — neither carries
// the full thought. By preferring paragraph boundaries (then sentence,
// then word) we keep ideas intact, which gives the retriever cleaner
// signal to match against.
//
// More sophisticated splitters exist (token-aware, semantic
// recursive-character, transformer-based), but this one works well
// and is sufficient for our purposes.
//
// "Approximately" because boundary-seeking trims the tail of a window;
// resulting chunks are typically 70–100% of size. Empty input
// produces no chunks.
//
// Indexing is byte-based for simplicity; the boundary patterns
// ("\n\n", ". ", " ") are ASCII, so cuts cannot land mid-rune unless
// the text contains a long run of non-ASCII with no whitespace —
// vanishingly rare for the course's source material.
func chunk(text string, size, overlap int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	if len(text) <= size {
		return []string{text}
	}

	// Defensive bounds: an overlap >= size would loop forever.
	if overlap < 0 {
		overlap = 0
	}

	if overlap >= size {
		overlap = size / 2
	}

	// Only accept a boundary if it lies in the last 30% of the window.
	// Without this threshold, an early "\n\n" would produce tiny
	// chunks; with it, the chunker prefers full-sized chunks.
	threshold := size * 7 / 10

	var chunks []string
	n := len(text)
	start := 0
	for start < n {
		end := start + size
		if end >= n {
			if part := strings.TrimSpace(text[start:]); part != "" {
				chunks = append(chunks, part)
			}
			break
		}

		window := text[start:end]
		switch {
		case strings.LastIndex(window, "\n\n") >= threshold:
			end = start + strings.LastIndex(window, "\n\n") + 2
		case strings.LastIndex(window, ". ") >= threshold:
			end = start + strings.LastIndex(window, ". ") + 2
		case strings.LastIndex(window, " ") >= threshold:
			end = start + strings.LastIndex(window, " ") + 1
		}

		if part := strings.TrimSpace(text[start:end]); part != "" {
			chunks = append(chunks, part)
		}
		start = end - overlap
	}

	return chunks
}
