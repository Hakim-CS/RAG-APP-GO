package rag

import (
	"fmt"
	"godocs-rag/llm"
	"strings"
)

const contextPreamble = `Use the following excerpts from the Go documentation to answer the question.
Cite sources by filename when you draw from them. If the excerpts do not address the question, say so.`

// formatContext renders hits as the excerpt block placed before the
// user's question. Image hits carry an [image: <path>] tag, which the
// system prompt tells the model to turn into Markdown image syntax.
func formatContext(hits []SearchResult) string {
	if len(hits) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(contextPreamble + "\n\n--- Excerpts ---\n\n")
	for i, h := range hits {
		source := h.Source
		if source == "" {
			source = "(unknown source)"
		}
		image := ""
		if h.ImagePath != "" {
			image = " [image: " + h.ImagePath + "]"
		}
		fmt.Fprintf(&sb, "[%d] Source: %s%s (similarity %.2f)\n%s\n\n", i+1, source, image, h.Score, h.Content)
	}
	return strings.TrimSpace(sb.String())
}

// WithContext returns a copy of history whose last user message is
// prefixed with contextText. history itself is not modified, so the
// caller's stored conversation keeps the user's original words.
func WithContext(history []llm.Message, contextText string) []llm.Message {
	if len(history) == 0 || contextText == "" {
		return history
	}
	last := history[len(history)-1]
	if last.Role != "user" {
		return history
	}
	out := make([]llm.Message, len(history))
	copy(out, history)
	out[len(out)-1] = llm.Message{
		Role:    "user",
		Content: contextText + "\n\n--- Question ---\n\n" + last.Content,
	}
	return out
}
