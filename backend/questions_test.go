package backend_test

import (
	"database/sql"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestQuestionOnlyCaptureKeepsOriginalAndSeparateAnswer(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Unresolved knowledge"}, restoredToken)
			if status != 200 {
				t.Fatal("project failed")
			}
			projectID := response["project"].(map[string]any)["id"].(string)
			status, response = captureRequest(t, origin, projectID, "question-retry", map[string]any{"type": "qa", "title": "Storage question", "authority": "explicit", "payload": map[string]any{"question": "Which database should hold optional RAG?"}})
			if status != 201 {
				t.Fatalf("question-only capture failed: %d %+v", status, response)
			}
			question := response["record"].(map[string]any)
			id := question["id"].(string)
			if question["content"] != "question: Which database should hold optional RAG?" {
				t.Fatal("question-only rendering invented an answer")
			}
			memory := projectMemory(t, origin, projectID)
			if len(memory["openQuestions"].([]any)) != 1 {
				t.Fatal("unresolved question missing")
			}
			status, response = captureRequest(t, origin, projectID, "", map[string]any{"type": "qa", "title": "Separate answer", "authority": "explicit", "payload": map[string]any{"question": "Which database should hold optional RAG?", "answer": "PostgreSQL, while SQLite keeps ordinary memory."}})
			if status != 201 {
				t.Fatal("answer capture failed")
			}
			answer := response["record"].(map[string]any)["id"].(string)
			status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": answer, "toRecordId": id, "type": "answers", "authority": "explicit"})
			if status != 201 {
				t.Fatal("accepted answer relationship failed")
			}
			memory = projectMemory(t, origin, projectID)
			if len(memory["openQuestions"].([]any)) != 0 {
				t.Fatal("accepted applicable answer did not resolve question")
			}
			status, response = curationRequest(t, origin, "GET", "/records/"+id, nil)
			if status != 200 || !reflect.DeepEqual(response["record"], question) {
				t.Fatal("answering changed the original question")
			}
		})
	}
}

func TestEmptyHistoricalAnswerEvidenceDoesNotResolveQuestion(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Answer evidence validation"}, restoredToken)
	if status != 200 {
		t.Fatal("project failed")
	}
	projectID := response["project"].(map[string]any)["id"].(string)
	status, response = captureRequest(t, origin, projectID, "", map[string]any{"type": "qa", "title": "Unanswered", "authority": "explicit", "payload": map[string]any{"question": "Where is the answer evidence?"}})
	if status != 201 {
		t.Fatal("question failed")
	}
	question := response["record"].(map[string]any)
	questionID := question["id"].(string)
	question["payload"].(map[string]any)["answer"] = " "
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, _ := json.Marshal(question)
	if _, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='record' AND id=$2`, string(raw), questionID); err != nil {
		t.Fatal(err)
	}
	if len(projectMemory(t, origin, projectID)["openQuestions"].([]any)) != 1 {
		t.Fatal("whitespace historical answer resolved the question")
	}
	status, response = captureRequest(t, origin, projectID, "", map[string]any{"type": "finding", "title": "Answer evidence", "authority": "observed", "payload": map[string]any{"finding": "Existing answer evidence."}})
	if status != 201 {
		t.Fatal("finding failed")
	}
	finding := response["record"].(map[string]any)
	findingID := finding["id"].(string)
	finding["content"] = ""
	finding["payload"] = map[string]any{"finding": ""}
	raw, _ = json.Marshal(finding)
	if _, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='record' AND id=$2`, string(raw), findingID); err != nil {
		t.Fatal(err)
	}
	status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": findingID, "toRecordId": questionID, "type": "answers", "authority": "explicit"})
	if status != 201 {
		t.Fatal("link failed")
	}
	if len(projectMemory(t, origin, projectID)["openQuestions"].([]any)) != 1 {
		t.Fatal("empty linked evidence resolved question")
	}
}

func TestRealStdioQuestionOnlyCaptureUsesGoBackend(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			private, key := githubSyntheticKey(t)
			enrollLocalSSH(t, origin, private, key, local)
			status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Real question client"}, restoredToken)
			if status != 200 {
				t.Fatal("project failed")
			}
			id := response["project"].(map[string]any)["id"].(string)
			cmd := exec.Command(integrationBinary, "-url", origin, "-public-key", private+".pub", "-signing-key", private, "-workspace", filepath.Dir(private), "-binary", mcpBinary, "-phase", "questions", "-project-id", id, "-state", filepath.Join(t.TempDir(), "questions.json"))
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "PASS questions") {
				t.Fatalf("real question capture failed: %v %s", err, out)
			}
		})
	}
}

func TestQuestionAnswerStatusUsesCurrentAcceptedEvidence(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Answer applicability"}, restoredToken)
			if status != 200 {
				t.Fatal("project failed")
			}
			projectID := response["project"].(map[string]any)["id"].(string)
			status, response = captureRequest(t, origin, projectID, "", map[string]any{"type": "qa", "title": "Question", "authority": "explicit", "payload": map[string]any{"question": "Which evidence is current?"}})
			if status != 201 {
				t.Fatal("question failed")
			}
			question := response["record"].(map[string]any)["id"].(string)
			status, response = captureRequest(t, origin, projectID, "", map[string]any{"type": "qa", "title": "Answer", "authority": "explicit", "payload": map[string]any{"question": "Which evidence is current?", "answer": "Only the applicable accepted answer."}})
			if status != 201 {
				t.Fatal("answer failed")
			}
			answer := response["record"].(map[string]any)["id"].(string)
			status, response = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": answer, "toRecordId": question, "type": "answers", "authority": "suggested"})
			if status != 201 {
				t.Fatal("proposal failed")
			}
			link := response["relationship"].(map[string]any)["id"].(string)
			if len(projectMemory(t, origin, projectID)["openQuestions"].([]any)) != 1 {
				t.Fatal("unaccepted suggestion resolved the question")
			}
			status, _ = curationRequest(t, origin, "POST", "/relationships/"+link+"/accept", map[string]any{"expectedVersion": 1})
			if status != 200 {
				t.Fatal("accept failed")
			}
			if len(projectMemory(t, origin, projectID)["openQuestions"].([]any)) != 0 {
				t.Fatal("accepted answer did not resolve question")
			}
			status, _ = curationRequest(t, origin, "PATCH", "/records/"+answer+"/metadata", map[string]any{"expectedVersion": 1, "archived": true})
			if status != 200 {
				t.Fatal("archive failed")
			}
			if len(projectMemory(t, origin, projectID)["openQuestions"].([]any)) != 1 {
				t.Fatal("archived answer still resolved question")
			}
			status, _ = curationRequest(t, origin, "PATCH", "/records/"+answer+"/metadata", map[string]any{"expectedVersion": 2, "archived": false})
			if status != 200 {
				t.Fatal("unarchive failed")
			}
			if len(projectMemory(t, origin, projectID)["openQuestions"].([]any)) != 0 {
				t.Fatal("restored applicable answer lost resolution")
			}
			newer := curationCapture(t, origin, projectID, "Replacement evidence")["id"].(string)
			status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": newer, "toRecordId": answer, "type": "replaces", "authority": "explicit"})
			if status != 201 {
				t.Fatal("replacement failed")
			}
			if len(projectMemory(t, origin, projectID)["openQuestions"].([]any)) != 1 {
				t.Fatal("superseded answer still resolved question")
			}
		})
	}
}
