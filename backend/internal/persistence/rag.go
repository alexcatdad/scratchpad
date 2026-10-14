package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrRAGUnavailable = errors.New("RAG requires application-schema pgvector")

// InitRAG applies the optional, serialized projection migration before workers
// start. Missing extension leaves the core schema and ordinary memory usable.
func (s *Store) InitRAG(ctx context.Context) error {
	if s.Engine != "postgres" {
		return ErrRAGUnavailable
	}
	return s.Write(ctx, func(conn *sql.Conn) error { return s.RAGSchema(ctx, conn) })
}

// RAGSchema requires the caller's existing serialized write transaction. It also
// supports an extension installed after startup without rewriting raw records.
func (s *Store) RAGSchema(ctx context.Context, conn *sql.Conn) error {
	var installed bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_extension WHERE extname='vector' AND extnamespace=(SELECT oid FROM pg_catalog.pg_namespace WHERE nspname=current_schema()))`).Scan(&installed); err != nil {
		return err
	}
	if !installed {
		return ErrRAGUnavailable
	}
	var marker, passages sql.NullString
	if err := conn.QueryRowContext(ctx, `SELECT to_regclass('rag_projection_migrations')::text,to_regclass('rag_passages')::text`).Scan(&marker, &passages); err != nil {
		return err
	}
	if !marker.Valid {
		if passages.Valid {
			return errors.New("malformed RAG projection")
		}
		for _, statement := range []string{
			`CREATE TABLE rag_projection_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`,
			`CREATE TABLE rag_passages(id TEXT NOT NULL,fingerprint TEXT NOT NULL,record_id TEXT NOT NULL,project_id TEXT NOT NULL,content_hash TEXT NOT NULL,processing_version TEXT NOT NULL,source_start INTEGER NOT NULL,source_end INTEGER NOT NULL,text TEXT NOT NULL,dimensions INTEGER NOT NULL,embedding vector NOT NULL,PRIMARY KEY(id,fingerprint),CHECK(source_start>=0 AND source_end>=source_start),CHECK(dimensions BETWEEN 1 AND 16000 AND vector_dims(embedding)=dimensions))`,
			`CREATE INDEX rag_passages_scope ON rag_passages(project_id,fingerprint,record_id)`,
		} {
			if _, err := conn.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO rag_projection_migrations VALUES(1,$1)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	var count, lowest, highest int
	if err := conn.QueryRowContext(ctx, `SELECT count(*),COALESCE(min(version),0),COALESCE(max(version),0) FROM rag_projection_migrations`).Scan(&count, &lowest, &highest); err != nil {
		return err
	}
	if count != 1 || lowest != 1 || highest != 1 {
		return errors.New("unsupported RAG projection schema")
	}
	var appliedAt string
	if err := conn.QueryRowContext(ctx, `SELECT applied_at FROM rag_projection_migrations WHERE version=1`).Scan(&appliedAt); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, appliedAt); err != nil {
		return errors.New("malformed RAG projection marker")
	}
	rows, err := conn.QueryContext(ctx, `SELECT id,fingerprint,record_id,project_id,content_hash,processing_version,source_start,source_end,text,dimensions,vector_dims(embedding) FROM rag_passages LIMIT 0`)
	if err != nil {
		return err
	}
	defer rows.Close()
	return rows.Err()
}
