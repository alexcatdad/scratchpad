package persistence

import (
	"context"
	"errors"
	"strings"
)

// This derivative holds admission/ordering fields, never immutable source bodies.
// Database maintenance keeps capture, import and operational restore atomic even
// when an older writer or a historical fixture writes the entities table directly.
const contextSpace = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"

func contextHeaderJSON(ref, engine string) string {
	fields := []string{"id", "projectId", "type", "createdAt", "recordedAt", "happenedAt"}
	parts := []string{}
	for _, field := range fields {
		expression := `json_extract(` + ref + `.data,'$.` + field + `')`
		if engine == "postgres" {
			expression = ref + `.data->'` + field + `'`
		}
		parts = append(parts, `'`+field+`',`+expression)
	}
	if engine == "postgres" {
		value := ref + `.data#>'{provenance,import}'`
		imported := `COALESCE(` + value + ` NOT IN ('null'::jsonb,'false'::jsonb,'0'::jsonb,'""'::jsonb),false)`
		answer := contextHeaderNonempty(ref, "payload.answer", engine)
		content := contextHeaderNonempty(ref, "content", engine)
		parts = append(parts, `'provenance',jsonb_build_object('import',`+imported+`)`, `'contextInlineAnswer',`+answer, `'contextContentNonempty',`+content)
		return `jsonb_build_object(` + strings.Join(parts, ",") + `)`
	}
	value := `json_extract(` + ref + `.data,'$.provenance.import')`
	imported := `CASE json_type(` + ref + `.data,'$.provenance.import') WHEN 'null' THEN 0 WHEN 'false' THEN 0 WHEN 'integer' THEN ` + value + `<>0 WHEN 'real' THEN ` + value + `<>0 WHEN 'text' THEN ` + value + `<>'' ELSE 1 END`
	// Missing paths return SQL NULL, so test them separately from JSON null.
	imported = `CASE WHEN json_type(` + ref + `.data,'$.provenance.import') IS NULL THEN 0 ELSE ` + imported + ` END`
	answer := contextHeaderNonempty(ref, "payload.answer", engine)
	content := contextHeaderNonempty(ref, "content", engine)
	parts = append(parts, `'provenance',json_object('import',json(CASE WHEN `+imported+` THEN 'true' ELSE 'false' END))`, `'contextInlineAnswer',json(CASE WHEN `+answer+` THEN 'true' ELSE 'false' END)`, `'contextContentNonempty',json(CASE WHEN `+content+` THEN 'true' ELSE 'false' END)`)
	return `json_object(` + strings.Join(parts, ",") + `)`
}

func contextHeaderNonempty(ref, path, engine string) string {
	if engine == "postgres" {
		jsonPath := "'{" + strings.ReplaceAll(path, ".", ",") + "}'"
		return "COALESCE(jsonb_typeof(" + ref + ".data#>" + jsonPath + ")='string' AND length(btrim(" + ref + ".data#>>" + jsonPath + ",'" + contextSpace + "'))>0,false)"
	}
	jsonPath := "'$." + path + "'"
	return "COALESCE(json_type(" + ref + ".data," + jsonPath + ")='text' AND trim(json_extract(" + ref + ".data," + jsonPath + "),'" + contextSpace + "')<>'',0)"
}

func contextHeaderProject(ref, engine string) string {
	if engine == "postgres" {
		return `CASE WHEN jsonb_typeof(` + ref + `.data->'projectId')='string' THEN ` + ref + `.data->>'projectId' ELSE '' END`
	}
	return `CASE WHEN json_type(` + ref + `.data,'$.projectId')='text' THEN json_extract(` + ref + `.data,'$.projectId') ELSE '' END`
}

func sqliteContextHeaderStatements() []string {
	insert := `INSERT INTO context_headers(record_id,project_id,data) SELECT NEW.id,` + contextHeaderProject("NEW", "sqlite") + `,` + contextHeaderJSON("NEW", "sqlite") + ` WHERE NEW.kind='record';`
	return []string{
		`CREATE TABLE context_headers(record_id TEXT PRIMARY KEY,project_id TEXT NOT NULL,data TEXT NOT NULL)`,
		`CREATE INDEX context_headers_project ON context_headers(project_id,record_id)`,
		`INSERT INTO context_headers(record_id,project_id,data) SELECT e.id,` + contextHeaderProject("e", "sqlite") + `,` + contextHeaderJSON("e", "sqlite") + ` FROM entities e WHERE e.kind='record'`,
		`CREATE TRIGGER context_headers_insert AFTER INSERT ON entities WHEN NEW.kind='record' BEGIN ` + insert + ` END`,
		`CREATE TRIGGER context_headers_update AFTER UPDATE ON entities WHEN OLD.kind='record' OR NEW.kind='record' BEGIN DELETE FROM context_headers WHERE record_id=OLD.id; ` + insert + ` END`,
		`CREATE TRIGGER context_headers_delete AFTER DELETE ON entities WHEN OLD.kind='record' BEGIN DELETE FROM context_headers WHERE record_id=OLD.id; END`,
	}
}

func postgresContextHeaderBody() string {
	return `BEGIN
 IF TG_OP <> 'INSERT' AND OLD.kind='record' THEN DELETE FROM context_headers WHERE record_id=OLD.id; END IF;
 IF TG_OP <> 'DELETE' AND NEW.kind='record' THEN
 INSERT INTO context_headers(record_id,project_id,data) VALUES(NEW.id,` + contextHeaderProject("NEW", "postgres") + `,` + contextHeaderJSON("NEW", "postgres") + `);
 END IF;
 RETURN NULL;
 END`
}

func postgresContextHeaderStatements() []string {
	return []string{
		`CREATE TABLE context_headers(record_id TEXT PRIMARY KEY,project_id TEXT NOT NULL,data JSONB NOT NULL)`,
		`CREATE INDEX context_headers_project ON context_headers(project_id,record_id)`,
		`INSERT INTO context_headers(record_id,project_id,data) SELECT e.id,` + contextHeaderProject("e", "postgres") + `,` + contextHeaderJSON("e", "postgres") + ` FROM entities e WHERE e.kind='record'`,
		`CREATE FUNCTION context_headers_sync() RETURNS trigger LANGUAGE plpgsql AS $context$` + postgresContextHeaderBody() + `$context$`,
		`CREATE TRIGGER context_headers_write AFTER INSERT OR UPDATE OR DELETE ON entities FOR EACH ROW EXECUTE FUNCTION context_headers_sync()`,
	}
}

func columnsForVersion(version int) map[string][]column {
	result := make(map[string][]column, len(sqliteColumns)+1)
	for name, columns := range sqliteColumns {
		result[name] = columns
	}
	if version >= 3 {
		result["context_headers"] = []column{{"record_id", "TEXT", 0, 1}, {"project_id", "TEXT", 1, 0}, {"data", "TEXT", 1, 0}}
	}
	return result
}

func validateContextHeaders(ctx context.Context, q queryer, engine string, version int) error {
	if version < 3 {
		return nil
	}
	if engine == "sqlite" {
		statements := sqliteContextHeaderStatements()
		names := []string{"context_headers_project", "context_headers_insert", "context_headers_update", "context_headers_delete"}
		expected := []string{statements[1], statements[3], statements[4], statements[5]}
		for i, name := range names {
			rows, err := q.QueryContext(ctx, `SELECT sql FROM sqlite_schema WHERE name=?`, name)
			if err != nil {
				return err
			}
			if !rows.Next() {
				rows.Close()
				return errors.New("missing context projection object")
			}
			var actual string
			err = rows.Scan(&actual)
			rows.Close()
			if err != nil {
				return err
			}
			if actual != expected[i] {
				return errors.New("malformed context projection object")
			}
		}
		return nil
	}
	rows, err := q.QueryContext(ctx, `SELECT p.prosrc FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname=current_schema() AND p.proname='context_headers_sync' AND p.pronargs=0 AND p.prorettype='trigger'::regtype AND l.lanname='plpgsql'`)
	if err != nil {
		return err
	}
	if !rows.Next() {
		rows.Close()
		return errors.New("missing context projection function")
	}
	var body string
	err = rows.Scan(&body)
	rows.Close()
	if err != nil {
		return err
	}
	if body != postgresContextHeaderBody() {
		return errors.New("malformed context projection function")
	}
	rows, err = q.QueryContext(ctx, `SELECT t.tgtype,t.tgenabled FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgname='context_headers_write' AND t.tgrelid='entities'::regclass AND p.proname='context_headers_sync' AND p.pronamespace=(SELECT oid FROM pg_namespace WHERE nspname=current_schema()) AND NOT t.tgisinternal`)
	if err != nil {
		return err
	}
	if !rows.Next() {
		rows.Close()
		return errors.New("missing context projection trigger")
	}
	var typ int
	var enabled string
	err = rows.Scan(&typ, &enabled)
	rows.Close()
	if err != nil {
		return err
	}
	// PostgreSQL flags: row(1), insert(4), delete(8), update(16); AFTER has no flag.
	if typ != 29 || enabled != "O" {
		return errors.New("malformed context projection trigger")
	}
	return nil
}
