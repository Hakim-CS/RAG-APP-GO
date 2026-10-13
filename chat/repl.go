// Package chat hosts the interactive terminal session. The REPL holds
// a persistent message history (system prompt + user/assistant turns)
// and replays it on every model call, because the chat-completions
// API is stateless — the server has no memory between requests.
//
// In lesson 1 the REPL is a plain read-eval-print loop. Later lessons
// will add a streaming variant, and an optional retriever that injects
// RAG context into each user turn.
package chat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"rag-course/llm"
	"rag-course/rag"
	"strings"
	"sync"
	"time"
)

// Options configures a single REPL session.
type Options struct {
	// SystemPromptFile is the path to a text/markdown file whose
	// contents become the conversation's system message. A missing
	// file is treated as "no system prompt" — not an error.
	SystemPromptFile string
}

// RunREPL drives an interactive chat session on stdin/stdout. Each
// line the user types is appended to a growing slice of llm.Messages
// and sent to the model; the reply is printed and then appended to the
// same history so subsequent turns retain context.
//
// The loop exits cleanly on "Q"/"q" or EOF. Per-turn API errors are
// printed and the loop continues; only unrecoverable stdin errors are
// returned.
//
// We add a simple spinner so there is some feedback while the system is
// "thinking".
func RunREPL(ctx context.Context, client *llm.Client, retriever *rag.Retriever, opts Options) error {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	history, err := seedHistory(opts.SystemPromptFile)
	if err != nil {
		return err
	}

	fmt.Println("Chat session started. Type Q to quit.")

	for {
		fmt.Print("\n> ")
		if !in.Scan() {
			if err := in.Err(); err != nil {
				return err
			}
			return nil
		}

		input := strings.TrimSpace(in.Text())
		if input == "" {
			continue
		}

		if strings.EqualFold(input, "q") || strings.EqualFold(input, "/exit") || strings.EqualFold(input, "exit") || strings.EqualFold(input, "quit") {
			fmt.Println("Goodbye.")
			return nil
		}

		history = append(history, llm.Message{Role: "user", Content: input})
		spin := startSpinner("thinking")
		var stopOnce sync.Once

		turn := history
		if retriever != nil {
			contextText, retErr := retriever.Retrieve(ctx, history)
			if retErr != nil {
				fmt.Fprintln(os.Stderr, "retrieval error: ", retErr)
			} else if contextText != "" {
				// build a turn with the inline context
				turn = withInlineContext(history, contextText)
			}
		}

		reply, err := client.ChatStream(ctx, turn, func(s string) {
			stopOnce.Do(spin.Stop)
			fmt.Print(s)
		})

		stopOnce.Do(spin.Stop)
		fmt.Println()

		if err != nil {
			fmt.Fprintln(os.Stderr, "error: ", err)
			// Roll back the user message so a retry doesn't
			// double-post it and so the failed turn doesn't pollute
			// future context.
			history = history[:len(history)-1]
			continue
		}

		history = append(history, reply)
	}
}

func withInlineContext(history []llm.Message, contextText string) []llm.Message {
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

// spinner renders a single-line animation on stdout until Stop is
// called. It clears the line on stop so subsequent output starts at
// column zero. Stop is safe to call multiple times and from multiple
// goroutines; only the first call has any effect.
type spinner struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// startSpinner starts the spinner
func startSpinner(label string) *spinner {
	s := &spinner{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		// frames for spinner
		frames := []string{"|", "/", "-", "\\"}
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Print("\r\033[K")
				return
			case <-t.C:
				fmt.Printf("\r%s %s", frames[i%len(frames)], label)
				i++
			}
		}
	}()
	return s
}

// Stop stops the spinner.
func (s *spinner) Stop() {
	s.once.Do(func() { close(s.stop) })
	<-s.done
}

// seedHistory builds the initial conversation slice. When a system
// prompt file is configured and present, its contents become the
// first message; otherwise the slice starts empty.
func seedHistory(path string) ([]llm.Message, error) {
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read system prompt: %w", err)
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, nil
	}

	return []llm.Message{{Role: "system", Content: content}}, nil
}
