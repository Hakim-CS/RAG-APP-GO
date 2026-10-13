// This is the Postgres-backed implementation of vector.Store using the pgvector
// extension. This is where we actually open a connection pool, install
// the extension, and run migrations.
//
// pgvector adds a "vector" data type to Postgres, plus distance
// operators (<->, <=>, <#>) and ANN index types (HNSW, IVFFlat). It
// turns a normal Postgres table into a competent vector database — no
// new infrastructure to run, you keep your existing tooling, backups,
// permissions, and SQL.
//
// On New, the store ensures the following schema exists:
//
//	CREATE EXTENSION IF NOT EXISTS vector;
//
//	CREATE TABLE IF NOT EXISTS documents (
//	    id          TEXT PRIMARY KEY,
//	    content     TEXT NOT NULL,
//	    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb,
//	    embedding   vector(<dim>) NOT NULL,
//	    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
//	);
//
//	CREATE INDEX IF NOT EXISTS documents_embedding_idx
//	    ON documents USING hnsw (embedding vector_cosine_ops);
//
// Reading the schema:
//
//	id           "<filename>#<chunk-index>", lets us upsert/delete
//	             specific chunks deterministically.
//	content      the chunk text, returned to the chat model as
//	             retrieval context.
//	metadata     JSONB so we can filter (e.g. metadata->>'source' =
//	             'tengu.txt') without changing the schema as new
//	             metadata fields appear.
//	embedding    fixed-dimension vector. The (<dim>) part below is
//	             literal in SQL — vector(1536) is a different type
//	             from vector(768).
//	hnsw + vector_cosine_ops
//	             HNSW (Hierarchical Navigable Small World) is an
//	             approximate-nearest-neighbor algorithm with great
//	             recall and fast queries. vector_cosine_ops tells
//	             pgvector to compare with cosine distance (the right
//	             metric for normalized embeddings). The alternatives
//	             are vector_l2_ops (Euclidean) and vector_ip_ops
//	             (inner product); cosine is the safe default.
//
// The vector dimension is supplied via Options.EmbeddingDim. It is
// baked into the column type at first migration; changing it later
// requires dropping and recreating the table. HNSW is used because it
// gives the best recall for the small-to-medium corpora typical in
// this course; switch to IVFFlat if you start ingesting millions of
// chunks.
//
// CREATE EXTENSION typically requires superuser; on managed Postgres
// (RDS, Cloud SQL, ...), the extension is usually pre-installed and
// the IF NOT EXISTS form is a no-op.
package pgvector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"rag-course/vector"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

// Options configures a Store.
type Options struct {
	// DSN is a libpq-style connection string,
	// e.g. "postgres://user:pass@host:5432/db?sslmode=disable".
	DSN string

	// EmbeddingDim is the column width for the embedding vector.
	// Must match the dimension produced by the configured embedder.
	EmbeddingDim int
}

// Store is the Postgres + pgvector implementation of vector.Store.
type Store struct {
	pool *pgxpool.Pool
}

// New connects to Postgres, registers the pgvector type with every
// pooled connection, and ensures the schema exists. It returns an
// error if the connection fails, the extension cannot be created, or
// the migration cannot run.
func New(ctx context.Context, opts Options) (*Store, error) {
	if opts.DSN == "" {
		return nil, errors.New("pgvector: DSN is required")
	}
	if opts.EmbeddingDim <= 0 {
		return nil, errors.New("pgvector: EmbeddingDim must be > 0")
	}

	cfg, err := pgxpool.ParseConfig(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}

	// Install the extension over a one-shot connection BEFORE opening
	// the pool. pgvector-go's RegisterTypes (wired into AfterConnect
	// below) looks up the vector OID at connect time, so the extension
	// must already exist or every pooled connection fails to come up
	// with "vector type not found in the database".
	if err := ensureExtension(ctx, opts.DSN); err != nil {
		return nil, fmt.Errorf("install extension: %w", err)
	}

	// Every new connection in the pool needs the vector OID registered
	// so pgx knows how to encode/decode the type. Without this the
	// driver returns the raw text representation.
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvec.RegisterTypes(ctx, conn)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	s := &Store{pool: pool}
	if err := s.migrate(ctx, opts.EmbeddingDim); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// ensureExtension installs the pgvector extension using a single
// throwaway connection that does not have RegisterTypes attached. This
// is the bootstrap step that lets the main pool's AfterConnect succeed
// on a fresh database.
func ensureExtension(ctx context.Context, dsn string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	return err
}

// migrate runs the idempotent schema setup. Each statement is safe to
// re-run, so this can execute on every startup. The CREATE EXTENSION
// step is handled by ensureExtension before the pool is opened.
func (s *Store) migrate(ctx context.Context, dim int) error {
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS documents (
		id   TEXT PRIMARY KEY,
		content  TEXT NOT NULL,
		metadata  JSONB NOT NULL DEFAULT '{}'::jsonb,
		embedding  vector(%d) NOT NULL,
		created_at   TIMESTAMPTZ NOT NULL DEFAULT now())
		`, dim),
		`CREATE INDEX IF NOT EXISTS documents_embedding_idx
		   ON documents USING hnsw (embedding vector_cosine_ops)`,
	}

	for _, q := range stmts {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("exec %q: %w", firstLine(q), err)
		}
	}

	return nil
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// Upsert inserts new rows or replaces existing ones by ID, in a single
// transaction so partial batches don't leak.
func (s *Store) Upsert(ctx context.Context, docs []vector.Document) error {
	if len(docs) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	const stmt = `
		INSERT INTO documents (id, content, metadata, embedding)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			content = EXCLUDED.content,
			metadata = EXCLUDED.metadata,
			embedding = EXCLUDED.embedding
	`

	for _, d := range docs {
		meta, err := marshalMetadata(d.Metadata)
		if err != nil {
			return fmt.Errorf("metadata for %s: %w", d.ID, err)
		}
		if _, err := tx.Exec(ctx, stmt, d.ID, d.Content, meta, pgvector.NewVector(d.Embedding)); err != nil {
			return fmt.Errorf("upsert: %s: %w", d.ID, err)
		}
	}

	return tx.Commit(ctx)
}

func marshalMetadata(m map[string]string) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}

	return json.Marshal(m)
}

func unmarshalMetadata(raw []byte, dst *map[string]string) error {
	if len(raw) == 0 {
		*dst = nil
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// Query returns the topK rows ranked by cosine similarity to the query
// embedding. The Score field is similarity in [-1, 1] (higher is more
// similar), derived from cosine distance via 1 - distance.
//
// The "<=>" operator is pgvector's cosine distance. Postgres's planner
// uses the HNSW index when ORDER BY uses the same operator the index
// was built on (vector_cosine_ops here), which is why the operator
// appears in both SELECT and ORDER BY.
//
// pgvector also offers "<->" (Euclidean / L2 distance) and "<#>"
// (negative inner product). Pick one and stick with it — the index is
// built for one metric.
func (s *Store) Query(ctx context.Context, embedding []float32, topK int) ([]vector.Result, error) {
	if topK <= 0 {
		return nil, nil
	}

	const stmt = `
		select id, content, metadata, embedding <=> $1 as distance
		from documents
		order by embedding <=> $1
		limit $2
	`

	rows, err := s.pool.Query(ctx, stmt, pgvector.NewVector(embedding), topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []vector.Result
	for rows.Next() {
		var (
			r        vector.Result
			metaRaw  []byte
			distance float64
		)

		if err := rows.Scan(&r.ID, &r.Content, &metaRaw, &distance); err != nil {
			return nil, err
		}
		if err := unmarshalMetadata(metaRaw, &r.Metadata); err != nil {
			return nil, fmt.Errorf("metadata for %s: %w", r.ID, err)
		}
		r.Score = float32(1 - distance)
		results = append(results, r)
	}

	return results, rows.Err()
}

// Delete removes documents by ID. Missing IDs are not an error — the
// DELETE simply matches zero rows for them.
func (s *Store) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	_, err := s.pool.Exec(ctx, `delete from documents where id = ANY($1)`, ids)
	return err
}

// DeleteBySource removes every row whose "source" metadata key
// matches source. The JSONB ->> operator compares as text, which is
// what we want — sources are filenames, not nested structures.
func (s *Store) DeleteBySource(ctx context.Context, source string) error {
	if source == "" {
		return nil
	}

	_, err := s.pool.Exec(ctx, `delete from documents where metadata->>'source' = $1`, source)
	return err
}

// Close releases the connection pool. Safe to call once; subsequent
// operations on the Store will fail.
func (s *Store) Close() error {
	s.pool.Close()
	return nil
}
