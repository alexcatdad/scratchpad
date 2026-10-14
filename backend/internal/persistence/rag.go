package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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
	return validateRAGSchema(ctx, conn)
}

// A readable empty projection does not establish that later writes and source
// filtering are safe. Check the installed migration's actual catalog contract,
// including unconstrained vector typmod: each configuration owns its dimensions.
func validateRAGSchema(ctx context.Context, conn *sql.Conn) error {
	expected := map[string][]string{
		"rag_projection_migrations": {"version:integer", "applied_at:text"},
		"rag_passages":              {"id:text", "fingerprint:text", "record_id:text", "project_id:text", "content_hash:text", "processing_version:text", "source_start:integer", "source_end:integer", "text:text", "dimensions:integer", "embedding:vector"},
	}
	for table, columns := range expected {
		rows, err := conn.QueryContext(ctx, `SELECT a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull, a.atthasdef,a.attgenerated,a.attidentity,c.relkind,c.relpersistence FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relname=$1 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`, table)
		if err != nil {
			return err
		}
		i := 0
		for rows.Next() {
			var name, kind, generated, identity, relation, durability string
			var notNull, hasDefault bool
			if err := rows.Scan(&name, &kind, &notNull, &hasDefault, &generated, &identity, &relation, &durability); err != nil {
				rows.Close()
				return err
			}
			if i >= len(columns) || name+":"+kind != columns[i] || !notNull || hasDefault || generated != "" || identity != "" || relation != "r" || durability != "p" {
				rows.Close()
				return errors.New("malformed RAG projection columns")
			}
			i++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if i != len(columns) {
			return errors.New("missing RAG projection columns")
		}
	}
	constraints := map[string]map[string]bool{
		"rag_projection_migrations": {"PRIMARY KEY (version)": true},
		"rag_passages": {
			"PRIMARY KEY (id, fingerprint)":                                                                     true,
			"CHECK (((source_start >= 0) AND (source_end >= source_start)))":                                    true,
			"CHECK ((((dimensions >= 1) AND (dimensions <= 16000)) AND (vector_dims(embedding) = dimensions)))": true,
		},
	}
	for table, wanted := range constraints {
		rows, err := conn.QueryContext(ctx, `SELECT k.contype,pg_get_constraintdef(k.oid),k.convalidated,k.condeferrable,k.connoinherit,COALESCE((to_jsonb(k)->>'conenforced')::boolean,true),COALESCE(i.indisvalid AND i.indisready,true) FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_class c ON c.oid=k.conrelid  JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_catalog.pg_index i ON i.indexrelid=k.conindid WHERE n.nspname=current_schema() AND c.relname=$1`, table)
		if err != nil {
			return err
		}
		for rows.Next() {
			var constraintType, definition string
			var validated, deferrable, noInherit, enforced, indexReady bool
			if err := rows.Scan(&constraintType, &definition, &validated, &deferrable, &noInherit, &enforced, &indexReady); err != nil {
				rows.Close()
				return err
			}
			// PostgreSQL 18 records NOT NULL constraints here; earlier versions
			// represent them only through attnotnull, already checked above.
			if constraintType == "n" && validated && enforced && !deferrable {
				continue
			}
			// pg_dump renders BETWEEN as two comparisons. Parsing that restored
			// DDL flattens the AND tree while preserving exactly the same check.
			if definition == "CHECK (((dimensions >= 1) AND (dimensions <= 16000) AND (vector_dims(embedding) = dimensions)))" {
				definition = "CHECK ((((dimensions >= 1) AND (dimensions <= 16000)) AND (vector_dims(embedding) = dimensions)))"
			}
			if !wanted[definition] || !validated || !enforced || !indexReady || deferrable || (strings.HasPrefix(definition, "CHECK") && noInherit) {
				rows.Close()
				return errors.New("malformed RAG projection constraints")
			}
			delete(wanted, definition)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(wanted) != 0 {
			return errors.New("missing RAG projection constraints")
		}
	}
	var valid bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class x ON x.oid=i.indexrelid JOIN pg_catalog.pg_class c ON c.oid=i.indrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_am am ON am.oid=x.relam WHERE n.nspname=current_schema() AND c.relname='rag_passages' AND x.relname='rag_passages_scope' AND i.indisvalid AND i.indisready AND NOT i.indisunique AND NOT i.indisprimary AND am.amname='btree' AND i.indnkeyatts=3 AND i.indnatts=3 AND i.indpred IS NULL AND i.indexprs IS NULL AND pg_get_indexdef(i.indexrelid,1,true)='project_id' AND pg_get_indexdef(i.indexrelid,2,true)='fingerprint' AND pg_get_indexdef(i.indexrelid,3,true)='record_id')`).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return errors.New("malformed RAG projection scope index")
	}
	rows, err := conn.QueryContext(ctx, `SELECT id,fingerprint,record_id,project_id,content_hash,processing_version,source_start,source_end,text,dimensions,vector_dims(embedding) FROM rag_passages LIMIT 0`)
	if err != nil {
		return err
	}
	defer rows.Close()
	return rows.Err()
}
