// Package config loads runtime configuration from environment
// variables. In lesson 1 there is exactly one thing to configure: how
// to reach an OpenAI-compatible chat endpoint (base URL, API key, and
// model name).
//
// Why env-driven and not flags or a YAML file? Three reasons:
//
//   - LLM apps usually run in containers or PaaS where env vars are
//     the universal config knob.
//   - Secrets (API keys) belong in env, not on the CLI.
//   - It's the simplest thing that works, and "the simplest thing
//     that works" is the rule for lesson 1.
//
// Later lessons will add embedding settings, a database URL, and a
// system-prompt path. Each one slots in as another field and another
// os.Getenv call — no architectural change required.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds settings for the LLM client. SystemPromptFile is here
// from the start because writing one good system prompt is a real
// part of the lesson; the rest of the project's settings will arrive
// in later lessons.
type Config struct {
	// BaseURL points at any OpenAI-compatible chat-completions
	// server: api.openai.com, a local Ollama at :11434/v1, LM
	// Studio, Groq, Together, vLLM, and so on. The wire protocol is
	// the same; only the URL and model name change.
	BaseURL string

	// APIKey is sent as `Authorization: Bearer <key>` when non-empty.
	// Local servers usually accept any value (or none); hosted
	// providers require their own key.
	APIKey string

	// Model is the chat-completions model identifier. Defaults to
	// gpt-4o-mini so a fresh OpenAI key works with no further setup.
	Model string

	// SystemPromptFile is the path to a text/markdown file whose
	// contents become the conversation's system message. A missing
	// file is silently treated as "no system prompt".
	SystemPromptFile string

	// DatabaseURL is the libpq-style DSN for the
	// Postgres + pgvector instance. Empty means "no vector store" —
	// chat still runs, just without retrieval. Populated from
	// DATABASE_URL.
	DatabaseURL string

	// EmbeddingDim is the dimensionality of the
	// embedding model that will populate the vector column. It is
	// baked into the column type at first migration (vector(1536) is
	// a different SQL type from vector(768)) and cannot be changed
	// afterward without recreating the table.
	//
	//   text-embedding-3-small  → 1536
	//   text-embedding-3-large  → 3072
	//   nomic-embed-text         → 768
	EmbeddingDim int

	// embedder endpoint config. EmbeddingBaseURL and
	// EmbeddingAPIKey let the embedder talk to a different OpenAI-
	// compatible endpoint than the chat client. The motivating case:
	// a hosted chat model (Ollama Cloud, OpenAI, Groq, ...) plus a
	// local embedder (Ollama, LM Studio, ...) — some hosted providers
	// do not expose embedding models. When EmbeddingBaseURL is empty,
	// the embedder reuses BaseURL/APIKey, preserving "one server for
	// everything" for the simple case.
	EmbeddingBaseURL string
	EmbeddingAPIKey  string

	EmbeddingModel string
	IngestDir      string
	ProcessedDir   string

	HTTPAddr    string
	ImageDir    string
	VisionModel string
}

// Validate checks configuration that would otherwise fail later at runtime.
// It intentionally does not require an API key because local Ollama-compatible
// servers commonly accept the conventional placeholder value.
func (c Config) Validate() error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return errors.New("OPENAI_BASE_URL must not be empty")
	}
	if strings.TrimSpace(c.Model) == "" {
		return errors.New("OPENAI_MODEL must not be empty")
	}
	if c.EmbeddingDim <= 0 {
		return errors.New("EMBEDDING_DIM must be greater than zero")
	}
	if c.EmbeddingModel == "" {
		return errors.New("EMBEDDING_MODEL must not be empty")
	}
	if c.HTTPAddr == "" {
		return errors.New("HTTP_ADDR must not be empty")
	}
	if c.IngestDir == c.ProcessedDir {
		return fmt.Errorf("INGEST_DIR and PROCESSED_DIR must differ")
	}
	return nil
}

// Load reads configuration from the environment, applying defaults
// for any value left unset. A .env file in the working directory is
// loaded first if present; values already exported in the process
// environment take precedence ("real env wins over .env" — the
// twelve-factor convention).
//
// Supported variables:
//
//	OPENAI_BASE_URL     defaults to https://api.openai.com/v1
//	OPENAI_API_KEY      no default; sent as a bearer token when set
//	OPENAI_MODEL        defaults to gpt-4o-mini
//	SYSTEM_PROMPT_FILE	no default
//	DATABASE_URL        no default; empty disables the vector store.
//	                    Example: postgres://rag:rag@localhost:5432/rag?sslmode=disable
//	EMBEDDING_DIM       defaults to 768 (matches nomic-embed-text).
func Load() Config {
	// A missing .env is not an error — fall back to the real process
	// environment so the program runs without one.
	_ = godotenv.Load()

	cfg := Config{
		BaseURL:          os.Getenv("OPENAI_BASE_URL"),
		APIKey:           os.Getenv("OPENAI_API_KEY"),
		Model:            os.Getenv("OPENAI_MODEL"),
		SystemPromptFile: os.Getenv("SYSTEM_PROMPT_FILE"),
		// Read DSN and embedding dimensionality
		// from the environment. atoiOr below converts the dim string
		// to int and falls back when the var is unset or malformed.
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		EmbeddingDim:     atoiOr(os.Getenv("EMBEDDING_DIM"), 0),
		EmbeddingBaseURL: os.Getenv("EMBEDDING_BASE_URL"),
		EmbeddingAPIKey:  os.Getenv("EMBEDDING_API_KEY"),
		EmbeddingModel:   os.Getenv("EMBEDDING_MODEL"),
		IngestDir:        os.Getenv("INGEST_DIR"),
		ProcessedDir:     os.Getenv("PROCESSED_DIR"),
		HTTPAddr:         os.Getenv("HTTP_ADDR"),
		ImageDir:         os.Getenv("IMAGES_DIR"),
		VisionModel:      os.Getenv("VISION_MODEL"),
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}

	if cfg.Model == "" {
		cfg.Model = "gpt-4o-mini"
	}

	// Default the embedding dimension to 768 to
	// match nomic-embed-text (the Ollama default this course is built
	// around) and the existing documents.embedding vector(768) column.
	// Switch to 1536 for OpenAI's text-embedding-3-small, or 3072 for
	// text-embedding-3-large — but note the dimension is baked into
	// the column type at first migration, so changing it later means
	// dropping and recreating the documents table.
	if cfg.EmbeddingDim == 0 {
		cfg.EmbeddingDim = 768
	}

	// When the user hasn't pointed the embedder at a
	// separate endpoint, reuse the chat endpoint and key — preserving
	// "one OpenAI-compatible server for everything" for the simple
	// case. When EMBEDDING_BASE_URL IS set we leave the API key alone:
	// a different host means a different (or no) credential, and
	// silently borrowing the chat key would send it to a server that
	// didn't ask for it.
	if cfg.EmbeddingBaseURL == "" {
		cfg.EmbeddingBaseURL = cfg.BaseURL
		if cfg.EmbeddingAPIKey == "" {
			cfg.EmbeddingAPIKey = cfg.APIKey
		}
	}

	if cfg.EmbeddingModel == "" {
		cfg.EmbeddingModel = "nomic-embed-text"
	}

	if cfg.IngestDir == "" {
		cfg.IngestDir = "./documents"
	}

	if cfg.ProcessedDir == "" {
		cfg.ProcessedDir = "./documents/processed"
	}

	if cfg.ImageDir == "" {
		cfg.ImageDir = "./documents/images"
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = "127.0.0.1:8080"
	}

	return cfg
}

// atoiOr parses s as an int, returning fallback
// when s is empty or invalid. Used so an unset EMBEDDING_DIM means
// "apply default" rather than zero.
func atoiOr(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}
