# Go RAG Engineering Assistant

A retrieval-augmented generation (RAG) application written in Go. The project combines an OpenAI-compatible language model, local document ingestion, and PostgreSQL with pgvector to provide answers grounded in a document collection.

This repository is an AI/ML engineering portfolio project focused on reliable retrieval, grounded responses, and reproducible local development. The current implementation uses vector retrieval and is being extended with structured results, evaluation, hybrid search, citations, and production safeguards.

The current development setup runs locally with Ollama and Docker:

- Ollama provides the chat and embedding models.
- PostgreSQL with pgvector stores document chunks and vector embeddings.
- The Go application handles ingestion, retrieval, terminal chat, and the web interface.

This is an actively developed project. The codebase is intentionally small and easy to follow while the retrieval, ingestion, and chat workflows evolve.

## Features

- Terminal chat through a simple REPL.
- Browser-based chat at `/chat`.
- Streaming responses in the terminal and web interface.
- Markdown and plain-text document ingestion.
- Automatic background watching of the `documents/` directory.
- Semantic search using embeddings and pgvector.
- Follow-up question rewriting for multi-turn conversations.
- Optional image uploads and vision-model captions.
- OpenAI-compatible model providers, including Ollama, OpenAI, LM Studio, and other compatible APIs.
- Configuration validation with safe local defaults.
- Health and readiness endpoints for service integration.
- Stable chunk metadata for traceable retrieval results.
- Automated Go tests, race detection, vetting, and formatting checks in GitHub Actions.

## Architecture

```text
                         +----------------------+
                         |      Go application  |
                         |  REPL + web server   |
                         +----------+-----------+
                                    |
                 +------------------+------------------+
                 |                                     |
                 v                                     v
        +------------------+                 +---------------------+
        | Ollama / API     |                 | PostgreSQL + pgvector|
        | chat + embeddings|                 | chunks + embeddings  |
        +------------------+                 +---------------------+
```

When a document is added, the application chunks it, creates embeddings, and stores both the text and vectors in PostgreSQL. When a user asks a question, the application embeds the question, retrieves the most relevant chunks, and sends those excerpts with the conversation to the chat model.

## Requirements

- Go 1.26 or later.
- Docker Desktop with Compose support.
- Ollama for the local development setup.
- Internet access in the browser for the web UI's CDN-hosted frontend libraries.

The default local models are:

| Purpose | Model | Dimensions |
| --- | --- | ---: |
| Chat and question rewriting | `gemma3` | N/A |
| Document and query embeddings | `nomic-embed-text` | 768 |
| Image captions | Optional vision model | N/A |

## Quick Start

### 1. Configure the environment

From the project root, create your local environment file:

```powershell
Copy-Item .env.example .env
```

Open `.env` and set a local PostgreSQL password. The password in `DATABASE_URL` must match `POSTGRES_PASSWORD`.

Never commit `.env`. It is ignored by Git. Use `.env.example` as the safe template for collaborators.

### 2. Install the local models

```powershell
ollama pull gemma3
ollama pull nomic-embed-text
```

An image-capable model is optional. Leave `VISION_MODEL` empty if image captioning is not needed.

### 3. Start PostgreSQL and pgvector

```powershell
docker compose up -d
docker compose ps
```

The database is exposed locally on port `5432`. The application creates the pgvector extension, documents table, and indexes on startup.

### 4. Run the application

```powershell
go run ./cmd/rag
```

Available interfaces:

- Terminal chat: use the prompt shown in the terminal.
- Web chat: <http://127.0.0.1:8080/chat>
- Health: <http://127.0.0.1:8080/healthz>
- Readiness: <http://127.0.0.1:8080/readyz>

Quit terminal chat with `q`, `quit`, `exit`, or `Ctrl+C`.

### 5. Add documents

Copy `.txt`, `.md`, or `.markdown` files into `documents/`. The watcher processes them automatically and moves completed files into `documents/processed/`.

The sample files already in `documents/processed/` belong to the repository, but they are not automatically loaded into a fresh database. To index them:

```powershell
Copy-Item .\documents\processed\*.txt, .\documents\processed\*.md .\documents\
```

## Configuration

Configuration is loaded by [config/config.go](config/config.go). Real environment variables take precedence over values in `.env`.

| Variable | Purpose |
| --- | --- |
| `OPENAI_BASE_URL` | Chat model endpoint. Ollama uses `http://localhost:11434/v1`. |
| `OPENAI_API_KEY` | Provider key. Ollama accepts the placeholder `ollama`; hosted providers require a real key. |
| `OPENAI_MODEL` | Chat model name, such as `gemma3:latest`. |
| `SYSTEM_PROMPT_FILE` | Markdown file used for the system prompt. |
| `POSTGRES_PASSWORD` | Password used by Docker Compose when initializing PostgreSQL. |
| `DATABASE_URL` | PostgreSQL connection string used by the application. |
| `EMBEDDING_BASE_URL` | Embedding endpoint. |
| `EMBEDDING_API_KEY` | Optional separate embedding-provider key. |
| `EMBEDDING_MODEL` | Embedding model name. |
| `EMBEDDING_DIM` | Vector width. It must match the embedding model and database schema. |
| `INGEST_DIR` | Directory watched for new documents. |
| `PROCESSED_DIR` | Directory for successfully processed documents. |
| `IMAGES_DIR` | Directory for uploaded images. |
| `HTTP_ADDR` | Web server address. Use `127.0.0.1:8080` for local-only access. |
| `VISION_MODEL` | Optional model used by `/api/caption`. |

### Using OpenAI instead of Ollama

The application can use OpenAI for chat and embeddings by changing the provider settings:

```env
OPENAI_BASE_URL=https://api.openai.com/v1
OPENAI_API_KEY=your-openai-key
OPENAI_MODEL=gpt-4o-mini

EMBEDDING_BASE_URL=https://api.openai.com/v1
EMBEDDING_API_KEY=your-openai-key
EMBEDDING_MODEL=text-embedding-3-small
EMBEDDING_DIM=1536
```

Do not mix vectors from different embedding models. If `EMBEDDING_DIM` changes, recreate the local database volume and ingest the documents again:

```powershell
docker compose down -v
docker compose up -d
```

The `down -v` command permanently deletes the local PostgreSQL data volume.

## HTTP API

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/chat` | Browser chat interface. |
| `POST` | `/api/chat/stream` | Streams a chat response as server-sent events. |
| `POST` | `/api/upload` | Uploads a `.txt` or `.md` document. |
| `POST` | `/api/upload/image` | Uploads an image and its description. |
| `POST` | `/api/caption` | Creates an image caption when `VISION_MODEL` is configured. |
| `GET` | `/images/*` | Serves uploaded images. |

## Project Structure

| Directory | Responsibility |
| --- | --- |
| `app/` | Application wiring and lifecycle management. |
| `chat/` | Terminal chat interface. |
| `cmd/rag/` | Executable entry point. |
| `config/` | Environment loading and defaults. |
| `ingest/` | Document chunking, embeddings, and filesystem watching. |
| `llm/` | OpenAI-compatible chat, embedding, and vision clients. |
| `rag/` | Retrieval, query rewriting, and prompt construction. |
| `vector/` | Storage interface and pgvector implementation. |
| `web/` | HTTP routes, uploads, streaming, and frontend template. |
| `documents/` | Local knowledge-base files and processed samples. |
| `prompts/` | System prompt files. |

## Development

Run the project tests and compiler checks with:

```powershell
go test ./...
```

Useful database commands:

```powershell
docker compose ps
docker compose logs -f postgres
docker compose stop
docker compose down
```

`docker compose down` removes the container but keeps the named database volume. `docker compose down -v` also deletes the stored database data.

## Current Limitations and Roadmap

- There are no automated tests yet for ingestion, retrieval, or HTTP behavior.
- Only plain text and Markdown documents are currently supported.
- Conversation history is kept in memory.
- The local web server does not provide authentication.
- Uploaded images and the local `.env` file are intentionally excluded from Git.

The next engineering milestones are hybrid keyword/vector retrieval, source citations, PDF and web ingestion, an offline evaluation dataset, authentication, rate limiting, metrics, and a hosted demo. The project deliberately remains a modular Go monolith while these capabilities are developed.

## Security Notes

- Never commit `.env`, API keys, passwords, or private documents.
- Use `127.0.0.1` for local development instead of exposing the web server on all interfaces.
- Treat the prompt-injection filter as a basic safeguard, not a complete security boundary.
- Rotate any key that has been committed to Git history before publishing the repository.

## License

MIT. See [LICENSE](LICENSE).
