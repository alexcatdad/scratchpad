package backend_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSemanticSearchReturnsExactImmutableNumericalSources(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			for _, fixture := range []struct{ name, payload string }{
				{"exact-and-historical-index", `{"finding":"SEMANTIC_NUMERICAL_SOURCE remains exact.","count":9007199254740993,"fraction":0.1234567890123456789012345}`},
				{"exponents-and-overflow", `{"finding":"SEMANTIC_NUMERICAL_SOURCE remains exact.","count":900719925474099300e-2,"fraction":1234567890123456789012345e-25,"huge":1e400}`},
			} {
				t.Run(fixture.name, func(t *testing.T) {
					testSemanticExactNumbers(t, engine, fixture.payload, fixture.name == "exact-and-historical-index")
				})
			}
		})
	}
}

func TestAnalysisExactNumericalSourcesPreserveFinalSourceFenceAndReadableArtifacts(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			provider := completionService(t, func(input map[string]any) any {
				record := input["records"].([]any)[0].(map[string]any)
				return map[string]any{"artifacts": []any{map[string]any{"kind": "summary", "title": "Numerical source summary", "content": map[string]any{"text": "Preserve immutable numerical evidence."}, "sourceRecordIds": []any{record["id"]}}}}
			})
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{"summary"}})
			if status != 200 {
				t.Fatal("analysis configuration failed")
			}
			project := jobProject(t, origin, "Exact numerical analysis", true, false)
			body := map[string]any{"projectId": project, "record": map[string]any{"type": "finding", "title": "Immutable numeric source", "authority": "observed", "payload": json.RawMessage(`{"finding":"Exact numerical evidence.","count":9007199254740993,"fraction":0.1234567890123456789012345,"huge":1e400}`)}}
			status, captured := numberCaptureRequest(t, origin, "POST", "/records", "analysis-numerical-source", body)
			if status != 201 {
				t.Fatal("capture failed")
			}
			id := captured["record"].(map[string]any)["id"].(string)
			status, before := numberCaptureRequest(t, origin, "GET", "/records/"+id, "", nil)
			if status != 200 {
				t.Fatal("source unavailable")
			}
			status, queued := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectId": project})
			if status != 202 {
				t.Fatal("analysis enqueue failed")
			}
			if job := waitProviderReviewJob(t, origin, queued["job"].(map[string]any)["id"].(string)); job["status"] != "completed" {
				t.Fatalf("exact final source fence rejected healthy numerical analysis: %v", job)
			}
			status, suggestions := curationRequest(t, origin, "GET", "/suggestions?projectId="+project, nil)
			if status != 200 || len(suggestions["suggestions"].([]any)) != 1 || !reflect.DeepEqual(suggestions["suggestions"].([]any)[0].(map[string]any)["sourceRecordIds"], []any{id}) {
				t.Fatalf("numerical artifact privacy/source validation failed: HTTP%d %v", status, suggestions)
			}
			status, after := numberCaptureRequest(t, origin, "GET", "/records/"+id, "", nil)
			if status != 200 || !reflect.DeepEqual(before["record"], after["record"]) {
				t.Fatal("analysis rewrote numerical immutable source")
			}
		})
	}
}

func testSemanticExactNumbers(t *testing.T, engine, payload string, historical bool) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer provider.Close()
	path, databaseURL := portableInstance(t, engine)
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("settings failed")
	}
	project := jobProject(t, origin, "Exact ordinary semantic evidence", true, false)
	body := map[string]any{"projectId": project, "record": map[string]any{"type": "finding", "title": "Immutable numeric source", "authority": "observed", "payload": json.RawMessage(payload)}}
	status, capture := numberCaptureRequest(t, origin, "POST", "/records", "semantic-exact-numbers", body)
	if status != 201 {
		t.Fatal("capture failed")
	}
	record := capture["record"].(map[string]any)
	status, detail := numberCaptureRequest(t, origin, "GET", "/records/"+record["id"].(string), "", nil)
	if status != 200 {
		t.Fatal("source detail unavailable")
	}
	record = detail["record"].(map[string]any)
	status, queued := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"projectId": project, "type": "embed"})
	if status != 202 {
		t.Fatal("embedding enqueue failed")
	}
	if job := waitProviderReviewJob(t, origin, queued["job"].(map[string]any)["id"].(string)); job["status"] != "completed" {
		t.Fatalf("exact numerical embedding failed: %v", job)
	}
	status, response := numberCaptureRequest(t, origin, "POST", "/search/semantic", "", map[string]any{"projectId": project, "query": "SEMANTIC_NUMERICAL_SOURCE"})
	if status != 200 || len(response["results"].([]any)) != 1 {
		t.Fatalf("semantic numerical evidence unavailable: HTTP%d %v", status, response)
	}
	returned := response["results"].([]any)[0].(map[string]any)["record"].(map[string]any)
	if !reflect.DeepEqual(record, returned) {
		t.Fatalf("semantic search altered immutable numerical evidence: expected payload %v, returned %v", record["payload"], returned["payload"])
	}
	if historical {
		driver, dsn := "sqlite", path
		if engine == "postgres" {
			driver, dsn = "pgx", databaseURL
		}
		db, err := sql.Open(driver, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		// Historical source hashes used float64 decoding. This supported
		// fixture alters only derivative hash metadata, never source rows.
		raw, _ := json.Marshal(record)
		var rounded map[string]any
		if json.Unmarshal(raw, &rounded) != nil {
			t.Fatal("historical fixture invalid")
		}
		raw, _ = json.Marshal(rounded)
		oldHash := fmt.Sprintf("%x", sha256.Sum256(raw))
		query := `UPDATE entities SET data=json_set(data,'$.contentHash',$1) WHERE kind='ai_embedding'`
		if engine == "postgres" {
			query = `UPDATE entities SET data=jsonb_set(data,'{contentHash}',to_jsonb($1::text)) WHERE kind='ai_embedding'`
		}
		if _, err = db.Exec(query, oldHash); err != nil {
			t.Fatal(err)
		}
		status, excluded := numberCaptureRequest(t, origin, "POST", "/search/semantic", "", map[string]any{"projectId": project, "query": "SEMANTIC_NUMERICAL_SOURCE"})
		if status != 200 || len(excluded["results"].([]any)) != 0 || excluded["indexRequired"] != true {
			t.Fatal("new exact index accepted rounded stale hash")
		}
		query = `UPDATE entities SET data=json_remove(data,'$.sourceHashVersion') WHERE kind='ai_embedding'`
		if engine == "postgres" {
			query = `UPDATE entities SET data=data-'sourceHashVersion' WHERE kind='ai_embedding'`
		}
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
		status, legacy := numberCaptureRequest(t, origin, "POST", "/search/semantic", "", map[string]any{"projectId": project, "query": "SEMANTIC_NUMERICAL_SOURCE"})
		if status != 200 || len(legacy["results"].([]any)) != 1 || !reflect.DeepEqual(record, legacy["results"].([]any)[0].(map[string]any)["record"]) {
			t.Fatalf("historical index was lost or altered source evidence: HTTP%d %v", status, legacy)
		}
	}
	status, detail = numberCaptureRequest(t, origin, "GET", "/records/"+record["id"].(string), "", nil)
	if status != 200 || !reflect.DeepEqual(record, detail["record"]) {
		t.Fatal("semantic indexing or compatibility rewrote source evidence")
	}
}
