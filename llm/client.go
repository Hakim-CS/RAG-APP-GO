// Package llm wraps the subset of the OpenAI-compatible chat-completions
// and embeddings APIs that this project needs, built on top of
// github.com/openai/openai-go/v3 — the official, generated SDK.
//
// Why one package, two APIs? Because almost every modern LLM provider
// (OpenAI, Ollama, LM Studio, Groq, Together, Anyscale, vLLM, ...)
// implements the same two endpoints:
//
//	chat completions   text in, text out (the model's reply)
//	embeddings         text in, dense vector out (numeric coords)
//
// Swap models or providers by changing OPENAI_BASE_URL and the model
// identifiers; no code changes required. The SDK honors any base URL
// that speaks the OpenAI wire format.
//
// Important conceptual point: the chat-completions API is STATELESS.
// The server forgets you between requests. The "conversation" only
// exists because the caller replays the full message history on every
// turn. Look at chat.RunREPL to see how that history is maintained.
package llm

import (
	"context"
	"rag-course/config"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Message is one entry in a chat conversation. Role is typically
// "system", "user", or "assistant"; Content is the text payload. We
// keep our own Message type (rather than re-exporting the SDK's
// ChatCompletionMessageParamUnion) so the rest of the codebase —
// internal/chat, internal/web, internal/rag — stays decoupled from any
// one SDK. Translation to the SDK's union type happens inside this
// package, in toSDKMessages.
//
// The three roles, in plain English:
//
//	system     hidden instruction prepended once at the start of a
//	           conversation — defines persona, scope, formatting
//	           rules. Lives in prompts/system.md in this project.
//	user       the human-typed message you want answered.
//	assistant  the model's reply. We append every reply back into
//	           history so the next turn has context to refer to.
//
// (Some APIs add a "tool" role for function-calling responses; we
// don't use it here.)
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client is a thin wrapper over the openai-go SDK client targeting one
// OpenAI-compatible endpoint. Construct with New; reuse across calls so
// the underlying HTTP client can pool connections.
type Client struct {
	cfg config.Config
	sdk openai.Client
}

// New returns a Client bound to the supplied configuration, using the
// chat endpoint (BaseURL/APIKey) to talk to the server. Non-empty
// values are forwarded to the SDK as explicit options; empty values
// are skipped so the SDK falls back to its own env-var defaults.
// APIKey in particular is skipped when empty because local providers
// (Ollama, LM Studio) don't require auth and a "Bearer " header with
// no token trips some servers.
//
// New is now the chat-side constructor. For embeddings,
// callers should use NewEmbedder so the embedder can target a
// different endpoint when EMBEDDING_BASE_URL is set.
func New(cfg config.Config) *Client {
	return newClient(cfg, cfg.BaseURL, cfg.APIKey)
}

//	NewEmbedder returns a Client whose underlying HTTP
//
// target is the embedding endpoint (EmbeddingBaseURL/EmbeddingAPIKey).
// Embed is the only method intended to be called on the resulting
// client; chat calls would go to the embedding-only server, which
// usually has no chat model.
//
// The split exists because some providers — Ollama Cloud at the time
// of writing — host chat models but no embedding models. Students who
// want to mix a hosted chat model with a local embedder can do so by
// setting EMBEDDING_BASE_URL without touching the chat path. When
// EMBEDDING_BASE_URL is empty, config falls it back to BaseURL, so
// this returns a client equivalent to New(cfg).
func NewEmbedder(cfg config.Config) *Client {
	return newClient(cfg, cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey)
}

// newClient builds a Client targeted at the supplied
// baseURL/apiKey. The full Config is retained so the caller can read
// back model identifiers (Model, EmbeddingModel) on later calls.
func newClient(cfg config.Config, baseURL, apiKey string) *Client {
	opts := []option.RequestOption{}

	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}

	return &Client{cfg: cfg, sdk: openai.NewClient(opts...)}
}

// ChatStream is the streaming counterpart to Chat. It opens a streaming
// chat-completions request and invokes onDelta with each content
// fragment as it arrives. The fully assembled Message is returned once
// the server signals completion.
//
// Why streaming matters for LLM UX: chat models generate
// token-by-token. A 500-token reply at 30 tokens/sec is ~17 seconds.
// Without streaming the user stares at a blank screen the whole
// time; with streaming they see words appear immediately. Same total
// latency, dramatically better perceived latency.
//
// SSE primer: the server keeps the HTTP response open and writes
// "data: <json>\n\n" lines as each token arrives. The SDK parses those
// lines for us — we just iterate the resulting stream and pull
// .Choices[0].Delta.Content out of each chunk.
//
// onDelta may be nil, in which case deltas are still accumulated but
// not surfaced during the stream.
func (c *Client) ChatStream(ctx context.Context, messages []Message, onDelta func(string)) (Message, error) {
	stream := c.sdk.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:    c.cfg.Model,
		Messages: toSDKMessages(messages),
	})
	defer stream.Close()

	var content strings.Builder
	role := "assistant"

	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Role != "" {
			role = delta.Role
		}

		if delta.Content != "" {
			content.WriteString(delta.Content)
			if onDelta != nil {
				onDelta(delta.Content)
			}
		}
	}

	if err := stream.Err(); err != nil {
		return Message{}, err
	}

	return Message{Role: role, Content: content.String()}, nil

}

// toSDKMessages converts our role-tagged Message slice to the SDK's
// param-union form. The default branch is a defensive fallback — every
// caller in this codebase tags messages "system", "user", or
// "assistant", so it should not fire in practice.
func toSDKMessages(messages []Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))

	for _, m := range messages {
		switch m.Role {
		case "system":
			out = append(out, openai.SystemMessage(m.Content))
		case "assistant":
			out = append(out, openai.AssistantMessage(m.Content))
		default:
			out = append(out, openai.UserMessage(m.Content))
		}
	}

	return out
}
