package persistence

import (
	"context"
	"errors"
	"strings"
)

// Migration 4 adds two tables that are paged by a database-allocated sequence.
//
// agent_events holds hook events from coding agents. They are evidence with
// their own retention, not captures: typed columns, no audit rows, and they
// are excluded from export.
//
// record_changes lists records in commit order. created_at cannot be a feed
// cursor: it is millisecond-precise with random identifiers, and imports keep
// historical timestamps, so a reader paging on it can skip rows. Every write
// goes through Store.Write, so sequence order equals commit order.
const (
	agentEventsIDIndex      = `CREATE UNIQUE INDEX agent_events_id ON agent_events(id)`
	agentEventsProjectIndex = `CREATE INDEX agent_events_project ON agent_events(project_id,seq)`
	recordChangesBackfill   = `INSERT INTO record_changes(record_id) SELECT id FROM entities WHERE kind='record' ORDER BY created_at,id`
	sqliteRecordChanges     = `CREATE TRIGGER record_changes_insert AFTER INSERT ON entities WHEN NEW.kind='record' BEGIN INSERT INTO record_changes(record_id) VALUES(NEW.id); END`
	postgresRecordChanges   = `BEGIN
 INSERT INTO record_changes(record_id) VALUES(NEW.id);
 RETURN NULL;
 END`
)

func sqliteAgentEventStatements() []string {
	return []string{
		`CREATE TABLE agent_events(seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL,project_id TEXT NOT NULL,credential_id TEXT NOT NULL,session_id TEXT NOT NULL,agent TEXT NOT NULL,kind TEXT NOT NULL,occurred_at TEXT NOT NULL,received_at TEXT NOT NULL,data TEXT NOT NULL)`,
		agentEventsIDIndex,
		agentEventsProjectIndex,
		`CREATE TABLE record_changes(seq INTEGER PRIMARY KEY AUTOINCREMENT,record_id TEXT NOT NULL)`,
		recordChangesBackfill,
		sqliteRecordChanges,
	}
}

func postgresAgentEventStatements() []string {
	return []string{
		`CREATE TABLE agent_events(seq BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,id TEXT NOT NULL,project_id TEXT NOT NULL,credential_id TEXT NOT NULL,session_id TEXT NOT NULL,agent TEXT NOT NULL,kind TEXT NOT NULL,occurred_at TEXT NOT NULL,received_at TEXT NOT NULL,data JSONB NOT NULL)`,
		agentEventsIDIndex,
		agentEventsProjectIndex,
		`CREATE TABLE record_changes(seq BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,record_id TEXT NOT NULL)`,
		recordChangesBackfill,
		`CREATE FUNCTION record_changes_append() RETURNS trigger LANGUAGE plpgsql AS $changes$` + postgresRecordChanges + `$changes$`,
		`CREATE TRIGGER record_changes_write AFTER INSERT ON entities FOR EACH ROW WHEN (NEW.kind='record') EXECUTE FUNCTION record_changes_append()`,
	}
}

var agentEventColumns = map[string][]column{
	"agent_events": {
		{"seq", "INTEGER", 0, 1}, {"id", "TEXT", 1, 0}, {"project_id", "TEXT", 1, 0}, {"credential_id", "TEXT", 1, 0}, {"session_id", "TEXT", 1, 0},
		{"agent", "TEXT", 1, 0}, {"kind", "TEXT", 1, 0}, {"occurred_at", "TEXT", 1, 0}, {"received_at", "TEXT", 1, 0}, {"data", "TEXT", 1, 0},
	},
	"record_changes": {{"seq", "INTEGER", 0, 1}, {"record_id", "TEXT", 1, 0}},
}

func validateAgentEvents(ctx context.Context, q queryer, engine string, version int) error {
	if version < 4 {
		return nil
	}
	if engine == "sqlite" {
		for name, expected := range map[string]string{
			"agent_events_id":       agentEventsIDIndex,
			"agent_events_project":  agentEventsProjectIndex,
			"record_changes_insert": sqliteRecordChanges,
		} {
			actual, err := scalarText(ctx, q, `SELECT sql FROM sqlite_schema WHERE name=?`, name)
			if err != nil {
				return err
			}
			if actual != expected {
				return errors.New("malformed agent event schema object")
			}
		}
		return nil
	}
	for name, suffix := range map[string]string{
		"agent_events_id":      "USING btree (id)",
		"agent_events_project": "USING btree (project_id, seq)",
	} {
		actual, err := scalarText(ctx, q, `SELECT indexdef FROM pg_indexes WHERE schemaname=current_schema() AND tablename='agent_events' AND indexname=$1`, name)
		if err != nil {
			return err
		}
		if !strings.HasSuffix(actual, suffix) || strings.HasPrefix(actual, "CREATE UNIQUE INDEX") != (name == "agent_events_id") {
			return errors.New("malformed agent event index")
		}
	}
	body, err := scalarText(ctx, q, `SELECT p.prosrc FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname=current_schema() AND p.proname='record_changes_append' AND p.pronargs=0 AND p.prorettype='trigger'::regtype AND l.lanname='plpgsql'`)
	if err != nil {
		return err
	}
	if body != postgresRecordChanges {
		return errors.New("malformed record change function")
	}
	rows, err := q.QueryContext(ctx, `SELECT t.tgtype,t.tgenabled,pg_get_triggerdef(t.oid) FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgname='record_changes_write' AND t.tgrelid='entities'::regclass AND p.proname='record_changes_append' AND p.pronamespace=(SELECT oid FROM pg_namespace WHERE nspname=current_schema()) AND NOT t.tgisinternal`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return errors.New("missing record change trigger")
	}
	var typ int
	var enabled, definition string
	if err = rows.Scan(&typ, &enabled, &definition); err != nil {
		return err
	}
	// PostgreSQL flags: row(1), insert(4); AFTER has no flag.
	if typ != 5 || enabled != "O" || !strings.Contains(definition, "WHEN ((new.kind = 'record'::text))") {
		return errors.New("malformed record change trigger")
	}
	return rows.Err()
}

func scalarText(ctx context.Context, q queryer, query string, args ...any) (string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return "", err
		}
		return "", errors.New("missing agent event schema object")
	}
	var value string
	if err = rows.Scan(&value); err != nil {
		return "", err
	}
	return value, rows.Err()
}
