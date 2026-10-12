package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"modernc.org/sqlite"
)

type Store struct {
	DB      *sql.DB
	Engine  string
	Version int
}

func (s *Store) Close() error { return s.DB.Close() }

func Open(ctx context.Context, path, databaseURL string) (*Store, error) {
	if databaseURL != "" {
		return openPostgres(ctx, databaseURL)
	}
	if path == "" {
		path = "data/scratchpad.sqlite"
	}
	if path != ":memory:" {
		var err error
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if err := privateSQLite(path); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + url.PathEscape(path) + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if path == ":memory:" {
		dsn = ":memory:?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, Engine: "sqlite"}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := enableWAL(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if path != ":memory:" {
		if err := privateSQLite(path); err != nil {
			db.Close()
			return nil, err
		}
		// WAL permits readers alongside the serialized BEGIN IMMEDIATE writer.
		// Keep startup single-connection and memory databases on their one handle.
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(4)
	}
	return s, nil
}

func enableWAL(ctx context.Context, db *sql.DB) error {
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		_, err := db.ExecContext(deadline, "PRAGMA journal_mode=WAL")
		if err == nil {
			return nil
		}
		var sqliteError *sqlite.Error
		if !errors.As(err, &sqliteError) || (sqliteError.Code()&255 != 5 && sqliteError.Code()&255 != 6) {
			return err
		}
		select {
		case <-deadline.Done():
			return deadline.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

type migration struct {
	version    int
	statements []string
}

var sqliteMigrations = []migration{{1, []string{
	`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`,
	`CREATE TABLE entities(kind TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,version INTEGER NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(kind,id))`,
	`CREATE TABLE identities(identity TEXT PRIMARY KEY,project_id TEXT NOT NULL)`,
	`CREATE TABLE retries(scope TEXT NOT NULL,key TEXT NOT NULL,request TEXT NOT NULL,response TEXT NOT NULL,PRIMARY KEY(scope,key))`,
	`CREATE VIRTUAL TABLE record_search USING fts5(record_id UNINDEXED,title,content)`,
}}, {2, []string{`CREATE INDEX entities_created ON entities(kind,created_at,id)`}}, {3, sqliteContextHeaderStatements()}}

func (s *Store) migrate(ctx context.Context) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	begin, inventory, insert := "BEGIN IMMEDIATE", `SELECT count(*) FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'`, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`
	migrations := sqliteMigrations
	if s.Engine == "postgres" {
		begin, inventory, insert = "BEGIN", `SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()`, `INSERT INTO schema_migrations(version,applied_at) VALUES($1,$2)`
		migrations = postgresMigrations
	}
	// BEGIN IMMEDIATE reserves the writer before inspecting the schema. A second
	// startup observes the first startup's committed marker, never a partial DDL set.
	if _, err := conn.ExecContext(ctx, begin); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if s.Engine == "postgres" {
		if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1397772880)`); err != nil {
			return err
		}
	}
	var count int
	if err := conn.QueryRowContext(ctx, inventory).Scan(&count); err != nil {
		return err
	}
	version := 0
	if count > 0 {
		version, err = validate(ctx, conn, s.Engine)
		if err != nil {
			return err
		}
	}
	for _, m := range migrations {
		if m.version <= version {
			continue
		}
		if m.version != version+1 {
			return errors.New("migration sequence")
		}
		for _, statement := range m.statements {
			if _, err := conn.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, insert, m.version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		version = m.version
	}
	if _, err := validate(ctx, conn, s.Engine); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	s.Version = version
	return nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func validate(ctx context.Context, q queryer, engine string) (int, error) {
	rows, err := q.QueryContext(ctx, `SELECT version,applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return 0, err
	}
	version := 0
	for rows.Next() {
		var v int
		var at string
		if err := rows.Scan(&v, &at); err != nil {
			rows.Close()
			return 0, err
		}
		if _, err := time.Parse(time.RFC3339Nano, at); err != nil || v != version+1 || v > len(sqliteMigrations) {
			rows.Close()
			return 0, errors.New("unsupported schema")
		}
		version = v
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if version == 0 {
		return 0, errors.New("missing schema marker")
	}
	queries := []string{
		`SELECT kind,id,data,version,created_at FROM entities LIMIT 0`,
		`SELECT identity,project_id FROM identities LIMIT 0`,
		`SELECT scope,key,request,response FROM retries LIMIT 0`,
		`SELECT record_id,title,content FROM record_search WHERE record_search MATCH 'scratchpad' LIMIT 0`,
	}
	if engine == "postgres" {
		queries[3] = `SELECT record_id,title,content,document FROM record_search WHERE document @@ plainto_tsquery('simple','scratchpad') LIMIT 0`
	}
	for _, query := range queries {
		rows, err := q.QueryContext(ctx, query)
		if err != nil {
			return 0, fmt.Errorf("schema object: %w", err)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
	}
	var shapeErr error
	if engine == "postgres" {
		shapeErr = validatePostgresColumns(ctx, q, version)
	} else {
		shapeErr = validateSQLiteColumns(ctx, q, version)
	}
	if shapeErr != nil {
		return 0, shapeErr
	}
	if err := validateContextHeaders(ctx, q, engine, version); err != nil {
		return 0, err
	}
	return version, nil
}

type column struct {
	name, kind    string
	required, key int
}

var sqliteColumns = map[string][]column{
	"schema_migrations": {{"version", "INTEGER", 0, 1}, {"applied_at", "TEXT", 1, 0}},
	"entities":          {{"kind", "TEXT", 1, 1}, {"id", "TEXT", 1, 2}, {"data", "TEXT", 1, 0}, {"version", "INTEGER", 1, 0}, {"created_at", "TEXT", 1, 0}},
	"identities":        {{"identity", "TEXT", 0, 1}, {"project_id", "TEXT", 1, 0}},
	"retries":           {{"scope", "TEXT", 1, 1}, {"key", "TEXT", 1, 2}, {"request", "TEXT", 1, 0}, {"response", "TEXT", 1, 0}},
}

func validateSQLiteColumns(ctx context.Context, q queryer, version int) error {
	for table, expected := range columnsForVersion(version) {
		rows, err := q.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
		if err != nil {
			return err
		}
		i := 0
		for rows.Next() {
			var cid, required, key int
			var name, kind string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &kind, &required, &defaultValue, &key); err != nil {
				rows.Close()
				return err
			}
			if i >= len(expected) || (column{name, strings.ToUpper(kind), required, key}) != expected[i] {
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
	if version >= 2 {
		rows, err := q.QueryContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='index' AND name='entities_created'`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var statement string
		if !rows.Next() {
			return errors.New("missing ordering index")
		}
		if err := rows.Scan(&statement); err != nil {
			return err
		}
		if strings.ToLower(strings.Join(strings.Fields(statement), "")) != "createindexentities_createdonentities(kind,created_at,id)" {
			return errors.New("malformed ordering index")
		}
		return rows.Err()
	}
	return nil
}

func (s *Store) Ready(ctx context.Context) error {
	_, err := validate(ctx, s.DB, s.Engine)
	return err
}

// Write serializes the same short transactions on both engines. Provider calls
// and possession-proof verification must complete before entering this boundary.
func (s *Store) Write(ctx context.Context, work func(*sql.Conn) error) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	begin := "BEGIN IMMEDIATE"
	if s.Engine == "postgres" {
		begin = "BEGIN"
	}
	if _, err := conn.ExecContext(ctx, begin); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if s.Engine == "postgres" {
		if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1397772880)`); err != nil {
			return err
		}
	}
	if err := work(conn); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}
