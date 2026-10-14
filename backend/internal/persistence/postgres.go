package persistence

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"
)

var postgresMigrations = []migration{{1, []string{
	`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`,
	`CREATE TABLE entities(kind TEXT NOT NULL,id TEXT NOT NULL,data JSONB NOT NULL,version INTEGER NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(kind,id))`,
	`CREATE INDEX entities_kind ON entities(kind)`,
	`CREATE TABLE identities(identity TEXT PRIMARY KEY,project_id TEXT NOT NULL)`,
	`CREATE TABLE retries(scope TEXT NOT NULL,key TEXT NOT NULL,request TEXT NOT NULL,response TEXT NOT NULL,PRIMARY KEY(scope,key))`,
	`CREATE TABLE record_search(record_id TEXT PRIMARY KEY,title TEXT NOT NULL,content TEXT NOT NULL,document TSVECTOR GENERATED ALWAYS AS(to_tsvector('simple',title || ' ' || content)) STORED)`,
	`CREATE INDEX record_search_document ON record_search USING GIN(document)`,
}}, {2, []string{`CREATE INDEX entities_created ON entities(kind,created_at,id)`}}, {3, postgresContextHeaderStatements()}}

func openPostgres(ctx context.Context, raw string) (*Store, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, errors.New("invalid database configuration")
	}
	values := u.Query()
	values.Set("connect_timeout", "10")
	// Encode writes spaces as "+", which the driver passes through literally,
	// turning options=-c role=x into "-c+role=x". Encode keeps a literal plus
	// as %2B, so every remaining "+" is a space.
	u.RawQuery = strings.ReplaceAll(values.Encode(), "+", "%20")
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(30 * time.Second)
	s := &Store{DB: db, Engine: "postgres"}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func validatePostgresColumns(ctx context.Context, q queryer, version int) error {
	columns := columnsForVersion(version)
	columns["record_search"] = []column{{"record_id", "TEXT", 0, 1}, {"title", "TEXT", 1, 0}, {"content", "TEXT", 1, 0}, {"document", "TSVECTOR", 0, 0}}
	for table, expected := range columns {
		rows, err := q.QueryContext(ctx, `SELECT c.column_name,c.data_type,c.is_nullable,COALESCE((SELECT k.ordinal_position FROM information_schema.key_column_usage k JOIN information_schema.table_constraints t USING(constraint_catalog,constraint_schema,constraint_name) WHERE t.constraint_type='PRIMARY KEY' AND k.table_schema=c.table_schema AND k.table_name=c.table_name AND k.column_name=c.column_name),0) FROM information_schema.columns c WHERE c.table_schema=current_schema() AND c.table_name=$1 ORDER BY c.ordinal_position`, table)
		if err != nil {
			return err
		}
		i := 0
		for rows.Next() {
			var name, kind, nullable string
			var key int
			if err := rows.Scan(&name, &kind, &nullable, &key); err != nil {
				rows.Close()
				return err
			}
			if i >= len(expected) {
				rows.Close()
				return errors.New("unexpected columns")
			}
			wanted := strings.ToLower(expected[i].kind)
			if (table == "entities" || table == "context_headers") && name == "data" {
				wanted = "jsonb"
			}
			wantedNullable := "NO"
			if table == "record_search" && expected[i].name == "document" {
				wantedNullable = "YES"
			}
			if name != expected[i].name || kind != wanted || nullable != wantedNullable || key != expected[i].key {
				rows.Close()
				return errors.New("malformed columns")
			}
			i++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if i != len(expected) {
			return errors.New("missing columns")
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT a.attgenerated,pg_get_expr(d.adbin,d.adrelid) FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE n.nspname=current_schema() AND c.relname='record_search' AND a.attname='document' AND NOT a.attisdropped`)
	if err != nil {
		return err
	}
	var generated, expression string
	if !rows.Next() {
		rows.Close()
		return errors.New("missing search document")
	}
	err = rows.Scan(&generated, &expression)
	rows.Close()
	if err != nil {
		return err
	}
	if generated != "s" || expression != `to_tsvector('simple'::regconfig, ((title || ' '::text) || content))` {
		return errors.New("malformed search document")
	}
	indexes := map[string]string{"entities_kind": "USING btree (kind)", "record_search_document": "USING gin (document)"}
	if version >= 2 {
		indexes["entities_created"] = "USING btree (kind, created_at, id)"
	}
	if version >= 3 {
		indexes["context_headers_project"] = "USING btree (project_id, record_id)"
	}
	for name, definition := range indexes {
		table := "entities"
		if name == "record_search_document" {
			table = "record_search"
		} else if name == "context_headers_project" {
			table = "context_headers"
		}
		rows, err := q.QueryContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname=current_schema() AND indexname=$1 AND tablename=$2`, name, table)
		if err != nil {
			return err
		}
		var actual string
		if !rows.Next() {
			rows.Close()
			return errors.New("missing index")
		}
		if err := rows.Scan(&actual); err != nil {
			rows.Close()
			return err
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if !strings.HasSuffix(actual, definition) {
			return errors.New("malformed index")
		}
	}
	return nil
}
