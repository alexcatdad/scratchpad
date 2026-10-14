package backend_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRAGAutomaticCaptureIndexesExactNumericSourceWithoutMutation(t *testing.T) {
	for _, fixture := range []struct{ name, payload string }{
		{"high-integer-and-precise-decimal", `{"finding":"NUMERIC_RAG_SOURCE remains exact.","count":9007199254740993,"fraction":0.1234567890123456789012345}`},
		{"exponents-and-overflow-sized-number", `{"finding":"NUMERIC_RAG_SOURCE remains exact.","count":900719925474099300e-2,"fraction":1234567890123456789012345e-25,"huge":1e400}`},
	} {
		t.Run(fixture.name, func(t *testing.T) { testRAGExactNumbers(t, fixture.payload) })
	}
}

func testRAGExactNumbers(t *testing.T, payload string) {
	path, databaseURL := ragInstance(t)
	var unavailable atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("settings failed")
	}
	project := jobProject(t, origin, "Exact numerical RAG", true, false)
	body := map[string]any{"projectId": project, "record": map[string]any{"type": "finding", "title": "Numerical immutable evidence", "authority": "observed", "payload": json.RawMessage(payload)}}
	status, captured := numberCaptureRequest(t, origin, "POST", "/records", "numeric-rag-capture", body)
	if status != 201 {
		t.Fatal("numeric capture failed")
	}
	record := captured["record"].(map[string]any)
	status, initialDetail := numberCaptureRequest(t, origin, "GET", "/records/"+record["id"].(string), "", nil)
	if status != 200 {
		t.Fatal("persisted numerical source unavailable")
	}
	persisted := initialDetail["record"].(map[string]any)
	fields := persisted["payload"].(map[string]any)
	if !exactRAGTestNumber(fields["count"], "9007199254740993") || !exactRAGTestNumber(fields["fraction"], "0.1234567890123456789012345") {
		t.Fatal("capture lost numerical precision")
	}
	if fields["huge"] != nil && !exactRAGTestNumber(fields["huge"], "1e400") {
		t.Fatal("capture lost overflow-sized numerical evidence")
	}
	queued := waitRAGJobs(t, origin, 1)[0].(map[string]any)
	job := waitProviderReviewJob(t, origin, queued["id"].(string))
	if job["status"] != "completed" {
		t.Fatalf("exact captured numbers permanently invalidated their queued RAG generation: %v", job)
	}
	status, coverage := curationRequest(t, origin, "GET", "/rag/coverage?projectId="+project, nil)
	if status != 200 || coverage["status"] != "complete" || coverage["indexedSources"] != float64(1) {
		t.Fatalf("exact source index coverage unavailable: HTTP%d %v", status, coverage)
	}
	status, evidence := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": project, "query": "NUMERIC_RAG_SOURCE"})
	if status != 200 || len(evidence["results"].([]any)) != 1 || evidence["semantic"].(map[string]any)["available"] != true {
		t.Fatalf("indexed numerical source unavailable: HTTP%d %v", status, evidence)
	}
	item := evidence["results"].([]any)[0].(map[string]any)
	if item["recordId"] != record["id"] || item["contentHash"] != job["contentHash"] || !strings.Contains(item["text"].(string), "9007199254740993") {
		t.Fatal("exact numerical citation or source identity changed")
	}
	status, detail := numberCaptureRequest(t, origin, "GET", "/records/"+record["id"].(string), "", nil)
	if status != 200 || !reflect.DeepEqual(persisted, detail["record"]) {
		t.Fatal("indexing mutated immutable numerical source")
	}
	status, rebuilt := curationRequest(t, origin, "POST", "/rag/index", map[string]any{"projectId": project, "recordId": record["id"], "rebuild": true, "expectedVersion": job["version"]})
	if status != 202 || rebuilt["job"].(map[string]any)["id"] != job["id"] {
		t.Fatalf("numerical rebuild changed source-generation identity: HTTP%d %v", status, rebuilt)
	}
	if retried := waitProviderReviewJob(t, origin, job["id"].(string)); retried["status"] != "completed" || retried["contentHash"] != job["contentHash"] {
		t.Fatalf("numerical rebuild failed: %v", retried)
	}
	unavailable.Store(true)
	status, fallback := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": project, "query": "NUMERIC_RAG_SOURCE"})
	if status != 200 || len(fallback["results"].([]any)) != 1 || fallback["semantic"].(map[string]any)["available"] != false {
		t.Fatalf("numerical immutable lexical fallback unavailable: HTTP%d %v", status, fallback)
	}
	status, detail = numberCaptureRequest(t, origin, "GET", "/records/"+record["id"].(string), "", nil)
	if status != 200 || !reflect.DeepEqual(persisted, detail["record"]) {
		t.Fatal("retry or fallback mutated numerical source")
	}
	if fields["huge"] != nil {
		// Historical captures hashed the incoming exponent spelling before JSONB
		// normalization. Seed that failed generation, then use only public APIs
		// to prove recovery without rewriting its provenance or immutable source.
		incoming, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		obsoleteHash := fmt.Sprintf("%x", sha256.Sum256(incoming))
		if obsoleteHash == job["contentHash"] {
			t.Fatal("historical exponent fixture did not differ from persisted source")
		}
		identity, _ := json.Marshal([]string{record["id"].(string), obsoleteHash, job["fingerprint"].(string)})
		obsoleteID := fmt.Sprintf("job_rag_%x", sha256.Sum256(identity))
		obsolete := make(map[string]any, len(job))
		for key, value := range job {
			obsolete[key] = value
		}
		obsolete["id"], obsolete["contentHash"], obsolete["status"] = obsoleteID, obsoleteHash, "failed"
		obsolete["lastError"] = "AI configuration changed; retry using current settings."
		encoded, _ := json.Marshal(obsolete)
		db, err := sql.Open("pgx", databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err = db.Exec(`UPDATE entities SET id=$1,data=$2 WHERE kind='ai_job' AND id=$3`, obsoleteID, string(encoded), job["id"]); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`DELETE FROM rag_passages WHERE record_id=$1`, record["id"]); err != nil {
			t.Fatal(err)
		}
		status, oldBefore := curationRequest(t, origin, "GET", "/ai/jobs?jobId="+obsoleteID, nil)
		if status != 200 || len(oldBefore["jobs"].([]any)) != 1 {
			t.Fatal("historical failed generation not publicly retained")
		}
		unavailable.Store(false)
		status, recovery := curationRequest(t, origin, "POST", "/rag/index", map[string]any{"projectId": project, "recordId": record["id"]})
		if status != 202 || recovery["job"].(map[string]any)["id"] != job["id"] {
			t.Fatalf("ordinary indexing did not recover stored numerical generation: HTTP%d %v", status, recovery)
		}
		if recovered := waitProviderReviewJob(t, origin, job["id"].(string)); recovered["status"] != "completed" || recovered["contentHash"] != job["contentHash"] {
			t.Fatalf("stored generation recovery failed: %v", recovered)
		}
		status, oldAfter := curationRequest(t, origin, "GET", "/ai/jobs?jobId="+obsoleteID, nil)
		if status != 200 || !reflect.DeepEqual(oldBefore["jobs"], oldAfter["jobs"]) {
			t.Fatal("explicit recovery rewrote historical failed job provenance")
		}
		status, coverage = curationRequest(t, origin, "GET", "/rag/coverage?projectId="+project, nil)
		if status != 200 || coverage["status"] != "complete" {
			t.Fatal("historical generation recovery left coverage incomplete")
		}
		status, detail = numberCaptureRequest(t, origin, "GET", "/records/"+record["id"].(string), "", nil)
		if status != 200 || !reflect.DeepEqual(persisted, detail["record"]) {
			t.Fatal("historical generation recovery mutated immutable source")
		}
	}
}

func exactRAGTestNumber(value any, expected string) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	actual, ok := new(big.Rat).SetString(number.String())
	want, valid := new(big.Rat).SetString(expected)
	return ok && valid && actual.Cmp(want) == 0
}
