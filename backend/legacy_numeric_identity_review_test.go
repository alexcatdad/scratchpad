package backend_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLegacyNumericIdentityMatchesFrozenImporterGoldenWithoutRoundingEvidence(t *testing.T) {
	expectedIDs := []string{"rec_import_7dc1b03262ff7e3260b3b48c1a932cdb", "rec_import_0019662da24309ccaa82795e402abe4b", "rec_import_90f5edc268591d04e81e3a17bdc2d8a5", "rec_import_cdbebfdc4dc3dc38e456e925f547be25", "rec_import_c0b903577bb0c95b5c1af374d7894ce5", "legacy_precise_retained", "rec_import_b191071429f5f6f8e024738188f26484", "rec_import_c073c570e7cb98817d1ae5bc3f41cbd3"}
	expectedHashes := []string{"ab940a09a4911cbaf7a964be0e1acc21898f63ccac6f191fdff893c86929b352", "fc1854e6636456c891471af01f70b49807fde68f452500fbba446e7f3389d56c", "cd8aec818b6f5470d9fda3200217bbdd51059d1a46e705907fbdbc0559dcb505", "f47316233db2593768415c7701cbd924e8fa24d04fdd22cb7ae3b2054e950779", "1adc1c11a63bc4c858e4a05ef28538a21242dcc446b568cb769be9ff3d43e3a1", "c75a5b67906c8ad705b5acd2e8ad6c552d10a341395db48c0c98e340150f1ff2", "23f8dced41a3aca8758ffa18bb3a5b17815ff676e99b265d0b0fb310b2d9119f", "a04a0c512c8938dbe9e48f76702da0833820b9c9a40de6452b90e609e068c390"}
	lines := []string{`{"finding":"Exponent evidence","n":1e2}`, `{"finding":"Decimal evidence","n":1.0}`, `{"finding":"Negative zero evidence","n":-0}`, `{"finding":"Small exponent evidence","n":1e-7}`, `{"finding":"Large exponent evidence","n":1e21}`, `{"id":"legacy_precise_retained","finding":"Exact raw evidence","payload":{"counter":9007199254740993}}`, `{"finding":"Anonymous precise identity","n":9007199254740993}`, `{"finding":"Overflow identity","n":1e400}`}
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			project := "proj_portable"
			status, _ := curationRequest(t, origin, "POST", "/import", portableArchive())
			if status != 200 {
				t.Fatal("fixed project fixture failed")
			}
			input := map[string]any{"format": "jsonl", "projectId": project, "sourceName": "numeric-parity.jsonl", "jsonl": strings.Join(lines, "\n")}
			status, result := curationRequest(t, origin, "POST", "/import", input)
			if status != 200 {
				t.Fatal("Go numerical import failed")
			}
			for i, value := range result["records"].([]any) {
				if value.(map[string]any)["recordId"] != expectedIDs[i] {
					t.Fatalf("numeric identity differs from frozen legacy output on line %d: got %v want %v project %s", i+1, value.(map[string]any)["recordId"], expectedIDs[i], project)
				}
				id := value.(map[string]any)["recordId"].(string)
				status, detail := numericArchiveRequest(t, origin, "GET", "/records/"+id, "")
				if status != 200 || detail["record"].(map[string]any)["provenance"].(map[string]any)["import"].(map[string]any)["sha256"] != expectedHashes[i] {
					t.Fatal("source fingerprint differs from frozen legacy output")
				}
			}
			id := result["records"].([]any)[0].(map[string]any)["recordId"].(string)
			status, detail := numericArchiveRequest(t, origin, "GET", "/records/"+id, "")
			if status != 200 || detail["record"].(map[string]any)["provenance"].(map[string]any)["import"].(map[string]any)["rawLine"] != lines[0] {
				t.Fatal("identity canonicalization rounded stored evidence")
			}
			status, precise := numericArchiveRequest(t, origin, "GET", "/records/legacy_precise_retained", "")
			if status != 200 || precise["record"].(map[string]any)["payload"].(map[string]any)["counter"] != json.Number("9007199254740993") {
				t.Fatal("identity canonicalization rounded precise payload evidence")
			}
			input["jsonl"] = `{"finding":"Exponent evidence","n":100}`
			status, result = curationRequest(t, origin, "POST", "/import", input)
			if status != 200 || result["imported"] != float64(0) || result["records"].([]any)[0].(map[string]any)["status"] != "skipped" {
				t.Fatal("equivalent numeric spelling did not replay")
			}
			input["jsonl"] = strings.Join(lines, "\n")
			status, archive := numericArchiveRequest(t, origin, "POST", "/export", `{}`)
			if status != 200 {
				t.Fatal("numeric archive export failed")
			}
			for _, withRawLine := range []bool{true, false} {
				freshPath, freshURL := portableInstance(t, engine)
				fresh := startServer(t, freshPath, freshURL)
				encoded, _ := json.Marshal(archive)
				var historical map[string]any
				json.Unmarshal(encoded, &historical)
				if !withRawLine {
					for _, value := range historical["data"].(map[string]any)["record"].([]any) {
						entry := value.(map[string]any)
						if entry["id"] == id {
							delete(entry["provenance"].(map[string]any)["import"].(map[string]any), "rawLine")
						}
					}
				}
				status, _ := curationRequest(t, fresh, "POST", "/import", historical)
				if status != 200 {
					t.Fatal("actual retained archive cannot restore into Go")
				}
				replayLines := lines
				if !withRawLine {
					replayLines = lines[:1]
				}
				status, replay := curationRequest(t, fresh, "POST", "/import", map[string]any{"format": "jsonl", "projectId": project, "sourceName": "numeric-parity.jsonl", "jsonl": strings.Join(replayLines, "\n")})
				if status != 200 || replay["imported"] != float64(0) || replay["skipped"] != float64(len(replayLines)) {
					t.Fatal("retained numerical archive replay failed")
				}
				for _, value := range replay["records"].([]any) {
					if value.(map[string]any)["status"] != "skipped" {
						t.Fatal("actual retained source numbers conflicted during replay")
					}
				}
			}
		})
	}
}
