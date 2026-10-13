// Package llm wraps the OpenAI-compatible chat-completions and
// embeddings APIs (via the official openai-go SDK), so the same code
// runs against Ollama, OpenAI, LM Studio, vLLM, etc. The SDK retries
// transient failures (429/5xx) with backoff by default.
package llm

import (
	"context"
	"godocs-rag/config"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Message is one chat turn. Role is "system", "user" or "assistant".
// The rest of the codebase uses this type rather than the SDK's union
// so only this package depends on the SDK.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client targets one OpenAI-compatible endpoint. Reuse it so the HTTP
// connection pool is shared.
type Client struct {
	cfg config.Config
	sdk openai.Client
}

// New returns a client for the chat endpoint (BaseURL/APIKey).
func New(cfg config.Config) *Client {
	return newClient(cfg, cfg.BaseURL, cfg.APIKey)
}

// NewEmbedder returns a client for the embedding endpoint, which may
// differ from the chat endpoint (e.g. hosted chat + local embedder).
func NewEmbedder(cfg config.Config) *Client {
	return newClient(cfg, cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey)
}

func newClient(cfg config.Config, baseURL, apiKey string) *Client {
	var opts []option.RequestOption
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	// An empty key is skipped: local servers need none, and a bare
	// "Bearer " header trips some of them.
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	return &Client{cfg: cfg, sdk: openai.NewClient(opts...)}
}

// ChatStream sends messages and calls onDelta (if non-nil) with each
// content fragment as it streams in. It returns the assembled reply.
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

// toSDKMessages maps roles to SDK messages. Unknown roles become user
// messages; untrusted callers must validate roles before this point.
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
