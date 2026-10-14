package backend_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func numberCaptureRequest(t *testing.T, origin, method, path, key string, body any) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	request, _ := http.NewRequest(method, origin+"/api/v1"+path, bytes.NewReader(data))
	request.Header.Set("Authorization", "Bearer "+restoredToken)
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	if decoder.Decode(&result) != nil {
		t.Fatal("non-JSON capture number response")
	}
	return response.StatusCode, result
}

func assertExactCaptureNumbers(t *testing.T, record map[string]any) {
	t.Helper()
	payload := record["payload"].(map[string]any)
	if payload["count"].(json.Number).String() != "9007199254740993" || payload["fraction"].(json.Number).String() != "0.1234567890123456789012345" || payload["nested"].([]any)[0].(json.Number).String() != "9007199254740992" {
		t.Fatal("capture payload rounded exact JSON numbers")
	}
	if record["gitContext"].(map[string]any)["counter"].(json.Number).String() != "9007199254740993" {
		t.Fatal("capture context rounded exact JSON number")
	}
	if !strings.Contains(record["content"].(string), "count: 9007199254740993") || !strings.Contains(record["content"].(string), "fraction: 0.1234567890123456789012345") {
		t.Fatal("rendered evidence rounded exact source numbers")
	}
}

func TestCaptureExactNumbersSurviveDetailsExportAndDurableRetry(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			var body map[string]any
			var id string
			for _, phase := range []string{"capture", "restart"} {
				t.Run(phase, func(t *testing.T) {
					origin := startServer(t, path, databaseURL)
					if phase == "capture" {
						status, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Exact numerical evidence"}, restoredToken)
						if status != 200 {
							t.Fatal("project unavailable")
						}
						body = map[string]any{"projectId": project["project"].(map[string]any)["id"], "gitContext": json.RawMessage(`{"branch":"main","counter":9007199254740993}`), "record": map[string]any{"type": "finding", "title": "Exact numbers", "authority": "observed", "payload": json.RawMessage(`{"finding":"Preserve numeric source evidence.","count":9007199254740993,"fraction":0.1234567890123456789012345,"nested":[9007199254740992,true,null]}`)}}
					}
					status, response := numberCaptureRequest(t, origin, "POST", "/records", "exact-number-request", body)
					if status != 201 {
						t.Fatalf("exact capture/replay failed: HTTP %d", status)
					}
					record := response["record"].(map[string]any)
					assertExactCaptureNumbers(t, record)
					if id != "" && record["id"] != id {
						t.Fatal("restart retry created another record")
					}
					id = record["id"].(string)
					status, detail := numberCaptureRequest(t, origin, "GET", "/records/"+id, "", nil)
					if status != 200 {
						t.Fatal("record detail unavailable")
					}
					assertExactCaptureNumbers(t, detail["record"].(map[string]any))
					status, archive := numberCaptureRequest(t, origin, "POST", "/export", "", map[string]any{})
					if status != 200 {
						t.Fatal("native export unavailable")
					}
					found := false
					for _, value := range archive["data"].(map[string]any)["record"].([]any) {
						if exported := value.(map[string]any); exported["id"] == id {
							assertExactCaptureNumbers(t, exported)
							found = true
						}
					}
					if !found {
						t.Fatal("captured record absent from export")
					}
					if phase == "capture" {
						original := body["record"].(map[string]any)["payload"].(json.RawMessage)
						body["record"].(map[string]any)["payload"] = json.RawMessage(strings.Replace(string(original), "9007199254740993", "9007199254740992", 1))
						status, _ = numberCaptureRequest(t, origin, "POST", "/records", "exact-number-request", body)
						if status != 409 {
							t.Fatal("distinct large payload numbers collapsed to one retry identity")
						}
						body["record"].(map[string]any)["payload"] = json.RawMessage(strings.Replace(string(original), "9007199254740993", "9007199254740993.0", 1))
						status, _ = numberCaptureRequest(t, origin, "POST", "/records", "exact-number-request", body)
						if status != 201 {
							t.Fatal("equivalent exact decimal notation changed retry identity")
						}
						body["record"].(map[string]any)["payload"] = original
						body["gitContext"] = json.RawMessage(`{"branch":"main","counter":9007199254740992}`)
						status, _ = numberCaptureRequest(t, origin, "POST", "/records", "exact-number-request", body)
						if status != 409 {
							t.Fatal("distinct large context numbers collapsed to one retry identity")
						}
						body["gitContext"] = json.RawMessage(`{"branch":"main","counter":9007199254740993}`)
					}
				})
			}
		})
	}
}
