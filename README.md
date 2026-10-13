# godocs-rag

A retrieval-augmented assistant for the Go standard library, written in Go on PostgreSQL + pgvector, **with a retrieval-quality eval that gates CI.**

Generic chatbots confidently invent Go APIs. This project answers from the actual package documentation, cites the file each answer came from, and refuses when the docs don't cover the question. Every retrieval change is measured against a hand-labelled question set before it can merge.

## Current results

Measured by `go run ./cmd/eval` over 117 in-scope and 12 out-of-scope questions (`eval/golden.jsonl`), 25 stdlib packages / 832 chunks, `nomic-embed-text`, k=5:

| Metric | Value |
| --- | ---: |
| Recall@1 | 0.299 |
| Recall@5 | 0.564 |
| MRR@5 | 0.405 |
| Out-of-scope abstention | 0.833 |
| Query latency p50 / p95 (embed + search, local Ollama) | 118 ms / 328 ms |

These are honest baseline numbers for plain vector search, not a finished system. The eval shows *where* it fails: in most misses the right package is retrieved but the wrong passage within it ranks higher, because dense `go doc` signature listings embed alike. That is what the next retrieval changes target (see [Roadmap](#roadmap)), and the gate makes each one prove itself.

## How it works

```text
 ingest:  corpus/*.txt ─▶ chunk (≈1000 B, boundary-aware) ─▶ embed (batched) ─▶ ReplaceSource (one tx) ─▶ pgvector
 query:   question ─▶ rewrite follow-ups ─▶ embed ─▶ HNSW cosine top-k ─▶ drop hits < min score ─▶ LLM (streamed)
```

- **Store:** one Postgres table with an HNSW cosine index and JSONB metadata. Re-ingesting a file replaces its chunks atomically: a failed embed or insert leaves the previous version intact.
- **Relevance floor:** chunks below `RETRIEVAL_MIN_SCORE` (0.55) are dropped. When nothing survives, the model is told to say the docs don't cover the question. The value was picked from the eval's sweep: it refuses 10 of 12 out-of-scope questions while losing 1 of 117 in-scope hits.
- **Providers:** any OpenAI-compatible endpoint (Ollama, OpenAI, LM Studio, vLLM). Chat and embeddings can point at different hosts.
- **Interfaces:** terminal REPL, web chat with SSE streaming at `/chat`, document and image upload, `/healthz` and `/readyz`.

### The eval

`cmd/eval` indexes `corpus/` into a throwaway database and runs every question in `eval/golden.jsonl` through the same `rag.Retriever` the app uses.

- A retrieved chunk is **relevant** when it comes from the labelled source file *and* contains the labelled phrase. Labelling by phrase instead of chunk ID keeps the set valid when chunking changes, and every label is checked against the corpus at startup, so a typo can't silently become a guaranteed miss.
- Out-of-scope questions have no source; they pass when retrieval returns nothing.
- CI fails if recall@1, recall@k, MRR or abstention falls more than 0.02 below `eval/baseline.json`. Improvements are accepted by re-running with `-write-baseline` and committing the new file.

`corpus/` is generated from the Go toolchain's own docs (`go run ./cmd/corpus`, which calls `go doc -all`) and committed, so the eval is reproducible offline.

## Quick start

Requirements: Go 1.26+, Docker, and [Ollama](https://ollama.com) (or any OpenAI-compatible provider).

```sh
cp .env.example .env            # set a local Postgres password in both places
ollama pull gemma3
ollama pull nomic-embed-text
docker compose up -d
cp corpus/*.txt documents/      # the watcher indexes and moves them to documents/processed/
go run ./cmd/rag
```

Then use the terminal prompt or open <http://127.0.0.1:8080/chat>.

### Running the eval

```sh
docker compose --profile eval up -d eval-db
go run ./cmd/eval                  # report + gate against eval/baseline.json
go run ./cmd/eval -skip-ingest     # reuse the indexed corpus while iterating
go run ./cmd/eval -sweep           # recall vs abstention for each min-score value
go run ./cmd/eval -write-baseline  # accept the current numbers
```

### Tests

```sh
go test -race ./...
# include the Postgres integration test:
TEST_DATABASE_URL=postgres://rag:eval@localhost:5433/rag_eval?sslmode=disable go test ./...
```

## Configuration

| Variable | Purpose |
| --- | --- |
| `OPENAI_BASE_URL`, `OPENAI_API_KEY`, `OPENAI_MODEL` | Chat endpoint. Ollama: `http://localhost:11434/v1`, key `ollama`. |
| `EMBEDDING_BASE_URL`, `EMBEDDING_API_KEY`, `EMBEDDING_MODEL` | Embedding endpoint; defaults to the chat endpoint. |
| `EMBEDDING_DIM` | Must match the embedding model (768 for `nomic-embed-text`). Fixed into the schema on first run. |
| `RETRIEVAL_MIN_SCORE` | Similarity floor for retrieved chunks (default 0.55, 0 disables). |
| `DATABASE_URL`, `POSTGRES_PASSWORD` | App database; the compose file reads the password. |
| `EVAL_DATABASE_URL` | Eval database (truncated on every run). |
| `SYSTEM_PROMPT_FILE` | System prompt, default `prompts/system-custom.md`. |
| `INGEST_DIR`, `PROCESSED_DIR`, `IMAGES_DIR` | Watched folder, archive folder, uploaded images. |
| `HTTP_ADDR` | Web server address (default `127.0.0.1:8080`). |
| `VISION_MODEL` | Optional model for `/api/caption`. |

Changing the embedding model or dimension requires a fresh table: `docker compose down -v` (this deletes the local database volume) and re-ingest.

## HTTP API

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/chat` | Browser chat UI. |
| `POST` | `/api/chat/stream` | `{"messages":[...]}` → SSE `delta` / `done` / `error` events. Only `user`/`assistant` roles are accepted. |
| `POST` | `/api/upload` | Ingest a `.txt` / `.md` file. |
| `POST` | `/api/upload/image` | Ingest an image by its description. |
| `POST` | `/api/caption` | Caption an image (when `VISION_MODEL` is set). |
| `GET` | `/images/{file}` | Uploaded images (no directory listing). |

## Project layout

| Path | Responsibility |
| --- | --- |
| `cmd/rag` | The app: REPL + web server + ingest watcher. |
| `cmd/eval` | Retrieval eval and CI gate. |
| `cmd/corpus` | Regenerates `corpus/` from `go doc`. |
| `app/` | Wiring and lifecycle. |
| `ingest/` | Chunking, embedding, directory watcher. |
| `rag/` | Query rewriting, retrieval, prompt context. |
| `vector/`, `vector/pgvector/` | Store interface and Postgres implementation. |
| `llm/` | OpenAI-compatible chat, embedding and vision clients. |
| `web/` | HTTP handlers, SSE, upload, injection filter, chat UI. |
| `eval/` | Golden question set and committed baseline. |

## Known limitations

- **The client owns the conversation history.** The server rejects client-sent `system` messages, but a client can still forge `assistant` turns. Server-side sessions would close this.
- **Uploads are unauthenticated**, and uploaded documents are retrieved into the prompt, so a malicious document can attempt indirect prompt injection. The regex filter in `web/injection.go` is a tripwire, not a security boundary. Bind to `127.0.0.1` (the default).
- Out-of-scope abstention is measured on only 12 questions; treat 0.833 as indicative.
- Chunks are byte windows; the overlap start isn't snapped to a word boundary.
- Conversation history is in memory only.

## Roadmap

Each step is judged by the eval, not by intuition:

1. **Hybrid retrieval:** Postgres full-text search fused with vector search via Reciprocal Rank Fusion. Targets the misses on exact identifiers (`func Atoi`, `ErrNoRows`).
2. **Embedding task prefixes and chunking:** `nomic-embed-text` expects `search_query:` / `search_document:` prefixes, which aren't applied yet. Also chunk on `go doc` declaration boundaries.
3. **Verified citations:** structured sources in the SSE stream, checked against the chunks actually supplied.
4. **Server-side sessions, upload auth, rate limiting.**

## License

MIT. See [LICENSE](LICENSE). `corpus/` is derived from Go documentation © The Go Authors, BSD-style licence.
