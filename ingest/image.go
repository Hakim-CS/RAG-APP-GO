package ingest

import (
	"context"
	"errors"
	"godocs-rag/llm"
	"godocs-rag/vector"
	"path/filepath"
	"strings"
)

// ImagePathPrefix is the URL prefix the web server serves images under.
const ImagePathPrefix = "/images/"

var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true,
}

func IsImage(name string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(name))]
}

// ProcessImage indexes an image by its text description; the chunks
// carry type=image and image_path so answers can render the picture.
func ProcessImage(ctx context.Context, name, description string, opts Options, embedder llm.Embedder, store vector.Store) (int, error) {
	base := filepath.Base(name)
	if !IsImage(base) {
		return 0, errors.New("unsupported image format")
	}
	desc := strings.TrimSpace(description)
	if desc == "" {
		return 0, errors.New("description is required")
	}
	return index(ctx, base, desc, map[string]string{
		"type":       "image",
		"image_path": ImagePathPrefix + base,
	}, opts, embedder, store)
}
