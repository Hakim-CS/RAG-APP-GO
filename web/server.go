package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"godocs-rag/ingest"
	"godocs-rag/llm"
	"godocs-rag/rag"
	"godocs-rag/vector"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed templates/*.gohtml
var templatesFS embed.FS

const maxUploadBytes = 10 << 20

type uploadResponse struct {
	Source string `json:"source"`
	Bytes  int    `json:"bytes"`
	Chunks int    `json:"chunks"`
}

type Options struct {
	SystemPromptFile string
	Title            string
	Store            vector.Store
	ProcessedDir     string
	ImagesDir        string
	Ready            bool
}

type Server struct {
	client       *llm.Client
	embedder     *llm.Client
	retriever    *rag.Retriever
	store        vector.Store
	processedDir string
	imagesDir    string
	tpl          *template.Template
	system       string
	title        string
	ready        bool
}

func New(client, embedder *llm.Client, retriever *rag.Retriever, opts Options) (*Server, error) {
	tpl, err := template.ParseFS(templatesFS, "templates/*.gohtml")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	title := opts.Title
	if title == "" {
		title = "Go Docs Assistant"
	}

	return &Server{
		client:       client,
		embedder:     embedder,
		retriever:    retriever,
		store:        opts.Store,
		processedDir: opts.ProcessedDir,
		imagesDir:    opts.ImagesDir,
		tpl:          tpl,
		system:       readSystemPrompt(opts.SystemPromptFile),
		title:        title,
		ready:        opts.Ready,
	}, nil
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Get("/chat", s.handleChatPage)

	r.Group(func(r chi.Router) {
		r.Use(InjectionDefense)
		r.Post("/api/chat/stream", s.handleChatStream)
		r.Post("/api/upload", s.handleUpload)
		if s.imagesDir != "" {
			r.Post("/api/upload/image", s.handleUploadImage)
		}
	})

	if s.imagesDir != "" {
		r.Handle("/images/*", http.StripPrefix("/images", noDirListing(http.FileServer(http.Dir(s.imagesDir)))))
	}

	if s.client.HasVision() {
		r.Post("/api/caption", s.handleCaption)
	}

	return r
}

// noDirListing 404s directory paths so the images directory can't be
// enumerated; individual files are still served.
func noDirListing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !s.ready {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"status":"not_ready","reason":"vector store unavailable"}`)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"status":"ready"}`)
}

type captionResponse struct {
	Description string `json:"description"`
}

func (s *Server) handleCaption(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, "upload too large or malformed: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "missing image field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if !ingest.IsImage(filepath.Base(header.Filename)) {
		http.Error(w, "unsupported image format", http.StatusUnsupportedMediaType)
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read upload: "+err.Error(), http.StatusBadRequest)
		return
	}

	mime := header.Header.Get("Content-Type")
	desc, err := s.client.DescribeImage(r.Context(), mime, content)
	if err != nil {
		log.Printf("[web] caption failed for %q: %v", header.Filename, err)
		http.Error(w, "caption failed", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(captionResponse{Description: strings.TrimSpace(desc)})
}

type uploadImageResponse struct {
	Source      string `json:"source"`
	ImagePath   string `json:"image_path"`
	Description string `json:"description"`
	Bytes       int    `json:"bytes"`
	Chunks      int    `json:"chunks"`
}

func (s *Server) handleUploadImage(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "ingest is not configured (no vector store)", http.StatusServiceUnavailable)
		return
	}

	if s.imagesDir == "" {
		http.Error(w, "image upload not configured", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, "upload too large or malformed: "+err.Error(), http.StatusBadRequest)
		return
	}

	description := strings.TrimSpace(r.FormValue("description"))
	if description == "" {
		http.Error(w, "description is required", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "missing 'image' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	original := filepath.Base(header.Filename)
	if !ingest.IsImage(original) {
		http.Error(w, "unsupported image format (allowed: .png, .jpg, .jpeg, .webp, .gif)", http.StatusUnsupportedMediaType)
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read upload: "+err.Error(), http.StatusBadRequest)
		return
	}

	saved := fmt.Sprintf("%d-%s", time.Now().UnixNano(), safeFileName(original))

	if err := os.MkdirAll(s.imagesDir, 0o755); err != nil {
		http.Error(w, "mkdir images dir: "+err.Error(), http.StatusInternalServerError)
		return
	}

	dest := filepath.Join(s.imagesDir, saved)
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		http.Error(w, "write image: "+err.Error(), http.StatusInternalServerError)
		return
	}

	chunks, err := ingest.ProcessImage(r.Context(), saved, description, ingest.Options{}, s.embedder, s.store)
	if err != nil {
		_ = os.Remove(dest)
		log.Printf("[web] image ingest failed for %q: %v", saved, err)
		http.Error(w, "ingest failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(uploadImageResponse{
		Source:      saved,
		ImagePath:   ingest.ImagePathPrefix + saved,
		Description: description,
		Bytes:       len(content),
		Chunks:      chunks,
	})

}

func safeFileName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	out := sb.String()
	if out == "" || out == "." || out == ".." {
		return "image"
	}
	return out
}

type chatRequest struct {
	Messages []llm.Message `json:"messages"`
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "ingest is not configured (no vector store)", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, "upload too large or malformed: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	name := filepath.Base(header.Filename)
	if !ingest.IsSupported(name) {
		http.Error(w, "unsupported format", http.StatusUnsupportedMediaType)
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read upload: "+err.Error(), http.StatusBadRequest)
		return
	}

	chunks, err := ingest.ProcessContent(r.Context(), name, content, ingest.Options{}, s.embedder, s.store)
	if err != nil {
		log.Printf("[web] upload ingest failed for %q: %v", name, err)
		http.Error(w, "ingest failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if s.processedDir != "" {
		dest := filepath.Join(s.processedDir, name)
		if err := os.MkdirAll(s.processedDir, 0o755); err != nil {
			log.Printf("[web] mkdir %s: %v", s.processedDir, err)
		} else if err := os.WriteFile(dest, content, 0o644); err != nil {
			log.Printf("[web] archive %s: %v", dest, err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(uploadResponse{
		Source: name,
		Bytes:  len(content),
		Chunks: chunks,
	})

}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := validateHistory(req.Messages); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	history := req.Messages
	if s.system != "" {
		history = append([]llm.Message{{Role: "system", Content: s.system}}, history...)
	}

	turn := history
	if s.retriever != nil {
		ctxText, err := s.retriever.Retrieve(r.Context(), history)
		if err != nil {
			log.Printf("[web] retrieval error: %v", err)
		} else {
			turn = rag.WithContext(history, ctxText)
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	send := func(event, data string) {
		if event != "" {
			fmt.Fprintf(w, "event: %s\n", event)
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	_, err := s.client.ChatStream(r.Context(), turn, func(delta string) {
		enc, _ := json.Marshal(delta)
		send("delta", string(enc))
	})
	if err != nil {
		enc, _ := json.Marshal(err.Error())
		send("error", string(enc))
		return
	}
	send("done", `""`)
}

// validateHistory enforces the shape of a client-supplied conversation.
// Only user and assistant turns are accepted: the system prompt is
// server-owned, and a client-sent "system" message would otherwise
// reach the model as a real instruction.
func validateHistory(msgs []llm.Message) error {
	if len(msgs) == 0 {
		return errors.New("messages must not be empty")
	}
	for i, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			return fmt.Errorf("messages[%d]: role must be \"user\" or \"assistant\"", i)
		}
	}
	if msgs[len(msgs)-1].Role != "user" {
		return errors.New("last message must be from user")
	}
	return nil
}

func (s *Server) handleChatPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, "chat.gohtml", map[string]any{
		"Title":          s.title,
		"CaptionEnabled": s.client.HasVision(),
	}); err != nil {
		log.Printf("[web] template error:  %v", err)
	}
}

func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Buffered so the goroutine can always deliver and exit, whichever
	// select branch runs.
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func readSystemPrompt(path string) string {
	if path == "" {
		return ""
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}

	return strings.TrimSpace(string(data))
}
