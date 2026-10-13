package backend_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestRecordDetailReturnsAllAssociatedAuditsAboveNativeBindBoundary(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			archive := portableArchive()
			data := archive["data"].(map[string]any)
			record := data["record"].([]any)[0].(map[string]any)
			other := map[string]any{}
			for field, value := range record {
				other[field] = value
			}
			other["id"] = "rec:other"
			data["record"] = append(data["record"].([]any), other)
			metadata := map[string]any{}
			for field, value := range data["metadata"].([]any)[0].(map[string]any) {
				metadata[field] = value
			}
			metadata["id"], metadata["recordId"] = "rec:other", "rec:other"
			data["metadata"] = append(data["metadata"].([]any), metadata)
			// SQLite's supported driver allows at most 32766 SQL parameters.
			// PostgreSQL uses a smaller fixture to exercise several audit batches
			// without paying for another tens-of-thousands-entity import in CI.
			count := 1201
			if engine == "sqlite" {
				count = 32767
			}
			entity := func(id string, fields map[string]any) map[string]any {
				fields["id"], fields["version"], fields["createdAt"] = id, 1, "2026-10-01T00:00:00.000Z"
				return fields
			}
			evidenceID := func(index int) string { return fmt.Sprintf("evidence_%05d", index) }
			evidence := make([]any, 0, 1201)
			for i := 0; i < 1201; i++ {
				evidence = append(evidence, entity(evidenceID(i), map[string]any{"recordId": "rec:portable", "kind": "url", "reference": "https://example.com/synthetic"}))
			}
			data["evidence"] = evidence
			// Cross-kind identities are supported by native archives. The shared
			// audit must appear once even if its identity spans audit batches.
			data["relationship"] = []any{entity(evidenceID(count-1), map[string]any{"fromRecordId": "rec:portable", "toRecordId": "rec:other", "type": "related_to", "status": "suggested", "authority": "suggested"})}
			audit := func(id, target, date string) map[string]any {
				value := entity(id, map[string]any{"entityId": target, "entityType": "evidence", "action": "synthetic.imported", "actor": map[string]any{"kind": "user"}})
				value["createdAt"] = date
				return value
			}
			data["audit"] = []any{
				audit("audit_z_record", "rec:portable", "2026-10-01T00:00:02.000Z"),
				audit("audit_b_middle", evidenceID(700), "2026-10-01T00:00:01.000Z"),
				audit("audit_a_first", evidenceID(0), "2026-10-01T00:00:01.000Z"),
				audit("audit_shared", evidenceID(count-1), "2026-10-01T00:00:00.000Z"),
				audit("audit_unrelated", "rec:other", "2026-10-01T00:00:00.000Z"),
			}
			status, imported := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("large valid native archive failed: HTTP%d %v", status, imported["error"])
			}
			if engine == "sqlite" {
				// Populate the remaining historical inputs in one fixture transaction.
				// Their shape is identical to the publicly imported evidence above;
				// avoid measuring native per-entity validation at this read boundary.
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				value := entity("evidence_fixture_placeholder", map[string]any{"recordId": "rec:portable", "kind": "url", "reference": "https://example.com/synthetic"})
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`WITH RECURSIVE historical(n) AS (
VALUES(1201) UNION ALL SELECT n+1 FROM historical WHERE n+1<$1
) INSERT INTO entities(kind,id,data,version,created_at)
SELECT 'evidence',printf('evidence_%05d',n),
replace($2,'evidence_fixture_placeholder',printf('evidence_%05d',n)),1,'2026-10-01T00:00:00.000Z'
FROM historical`, count, string(raw))
				if err != nil {
					t.Fatal(err)
				}
			}
			status, detail := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			if status != 200 {
				t.Fatalf("record with %d associated entities is unreadable: HTTP%d %v", count+1, status, detail["error"])
			}
			if !reflect.DeepEqual(detail["record"], record) || len(detail["evidence"].([]any)) != count || len(detail["relationships"].([]any)) != 1 {
				t.Fatal("detail omitted associations or changed the immutable record")
			}
			got := []string{}
			for _, event := range detail["audit"].([]any) {
				got = append(got, event.(map[string]any)["id"].(string))
			}
			want := []string{"audit_shared", "audit_a_first", "audit_b_middle", "audit_z_record"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("associated audits lost deterministic ordering, duplication or scope: got%v want%v", got, want)
			}
		})
	}
}
