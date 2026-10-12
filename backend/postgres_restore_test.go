package backend_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Use matching clients from the owned CI service, or local PostgreSQL clients.
// Credentials travel in environment variables, never command arguments/output.
func postgresArchiveCommand(t *testing.T, tool, databaseURL string, args ...string) *exec.Cmd {
	t.Helper()
	u, err := url.Parse(databaseURL)
	if err != nil || u.User == nil {
		t.Fatal("invalid operational restore fixture connection")
	}
	password, _ := u.User.Password()
	if tool == "pg_restore" {
		args = append(args, "--dbname="+strings.TrimPrefix(u.Path, "/"))
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	host := u.Hostname()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	var cmd *exec.Cmd
	if container := os.Getenv("TEST_POSTGRES_CONTAINER_ID"); container != "" {
		host, port = "127.0.0.1", "5432"
		dockerArgs := []string{"exec", "-i"}
		for _, name := range []string{"PGHOST", "PGPORT", "PGUSER", "PGPASSWORD", "PGDATABASE", "PGSSLMODE"} {
			dockerArgs = append(dockerArgs, "--env", name)
		}
		dockerArgs = append(dockerArgs, container, tool)
		cmd = exec.CommandContext(ctx, "docker", append(dockerArgs, args...)...)
	} else {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is required when TEST_POSTGRES_URL is configured", tool)
		}
		cmd = exec.CommandContext(ctx, tool, args...)
	}
	sslmode := u.Query().Get("sslmode")
	if sslmode == "" {
		sslmode = "prefer"
	}
	cmd.Env = append(os.Environ(), "PGHOST="+host, "PGPORT="+port, "PGUSER="+u.User.Username(), "PGPASSWORD="+password, "PGDATABASE="+strings.TrimPrefix(u.Path, "/"), "PGSSLMODE="+sslmode)
	return cmd
}

func TestPostgresOperationalRestoreRetainsEncryptedSettingsAndRetries(t *testing.T) {
	postgresOperationalRestore(t, false)
}

func TestRAGPostgresOperationalRestoreRetainsPassageProjection(t *testing.T) {
	postgresOperationalRestore(t, true)
}

func postgresOperationalRestore(t *testing.T, rag bool) {
	t.Helper()
	if rag {
		if os.Getenv("TEST_PGVECTOR_URL") == "" {
			t.Skip("TEST_PGVECTOR_URL is required for native projection restore")
		}
		t.Setenv("TEST_POSTGRES_URL", os.Getenv("TEST_PGVECTOR_URL"))
	} else if os.Getenv("TEST_POSTGRES_URL") == "" {
		t.Skip("TEST_POSTGRES_URL is required for operational restore")
	}
	private, public := githubSyntheticKey(t)
	var calls atomic.Int32
	const secret = "synthetic-postgres-restore-provider-token"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unexpected synthetic credential", 401)
			return
		}
		calls.Add(1)
		if r.URL.Path == "/v1/embeddings" {
			w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
		} else {
			w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
		}
	}))
	defer provider.Close()
	key := syntheticEncryptionKey(t, 83)
	var archive []byte
	var record map[string]any
	var projectID string
	input := map[string]any{"type": "finding", "title": "RESTORE_EVIDENCE_WITNESS", "authority": "observed", "payload": map[string]any{"finding": "Keep the original restore evidence and its durable retry identity."}}
	if !t.Run("live-source-snapshot", func(t *testing.T) {
		t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", key)
		var path, databaseURL string
		if rag {
			path, databaseURL = ragInstance(t)
		} else {
			path, databaseURL = portableInstance(t, "postgres")
		}
		origin := startServer(t, path, databaseURL)
		enrollLocalSSH(t, origin, private, public, &http.Cookie{Name: "scratchpad_session", Value: "synthetic-independent-browser-session"})
		status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "apiKey": secret, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2, "scheduleMinutes": 0})
		if status != 200 {
			t.Fatal("encrypted settings fixture failed")
		}
		projectID = jobProject(t, origin, "Operational PostgreSQL restore", rag, false)
		status, captured := captureRequest(t, origin, projectID, "postgres-restore-retry", input)
		if status != 201 {
			t.Fatal("source capture failed")
		}
		record = captured["record"].(map[string]any)
		if rag {
			jobs := waitRAGJobs(t, origin, 1)
			waitAIJob(t, origin, jobs[0].(map[string]any)["id"].(string), "completed")
		}
		var err error
		archive, err = postgresArchiveCommand(t, "pg_dump", databaseURL, "--format=custom", "--compress=0", "--no-owner", "--no-acl").Output()
		if err != nil || !bytes.HasPrefix(archive, []byte("PGDMP")) {
			t.Fatal("operational PostgreSQL dump failed")
		}
		if bytes.Contains(archive, []byte(secret)) {
			t.Fatal("uncompressed database archive exposed the reusable provider token")
		}
		curationCapture(t, origin, projectID, "LATER_THAN_RESTORE_SNAPSHOT")
	}) {
		return
	}
	if len(archive) == 0 { // The optional database fixture was skipped.
		return
	}
	destination := isolatedPostgres(t)
	restore := postgresArchiveCommand(t, "pg_restore", destination, "--exit-on-error", "--no-owner", "--no-acl")
	restore.Stdin = bytes.NewReader(archive)
	if err := restore.Run(); err != nil {
		t.Fatal("restore into a fresh PostgreSQL database failed")
	}
	for _, phase := range []string{"retained-key", "wrong-key", "missing-key"} {
		t.Run(phase, func(t *testing.T) {
			file := key
			if phase == "wrong-key" {
				file = syntheticEncryptionKey(t, 84)
			} else if phase == "missing-key" {
				file = ""
			}
			t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", file)
			if rag {
				t.Setenv("SCRATCHPAD_RAG_ENABLED", "true")
			}
			origin := startServer(t, "", destination)
			_, proof := signedChallenge(t, origin, private, public)
			status, session := postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
			if status != 200 || session["accessToken"] == nil {
				t.Fatal("restored independent SSH key could not prove possession")
			}
			request, _ := http.NewRequest("GET", origin+"/api/v1/records/"+record["id"].(string), nil)
			request.Header.Set("Authorization", "Bearer "+session["accessToken"].(string))
			machineRead, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal("restored machine session could not read memory")
			}
			machineRead.Body.Close()
			if machineRead.StatusCode != 200 {
				t.Fatal("fresh restored machine authentication was not usable")
			}
			status, detail := curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
			if status != 200 || !reflect.DeepEqual(detail["record"], record) {
				t.Fatal("restored credentials or original record changed")
			}
			status, replay := captureRequest(t, origin, projectID, "postgres-restore-retry", input)
			if status != 201 || !reflect.DeepEqual(replay["record"], record) {
				t.Fatal("restore lost durable capture retry identity")
			}
			status, found := searchRecords(t, origin, "q=RESTORE_EVIDENCE_WITNESS")
			if status != 200 || len(found["records"].([]any)) != 1 || found["records"].([]any)[0].(map[string]any)["id"] != record["id"] {
				t.Fatal("restore lost native lexical evidence")
			}
			status, later := searchRecords(t, origin, "q=LATER_THAN_RESTORE_SNAPSHOT")
			if status != 200 || len(later["records"].([]any)) != 0 {
				t.Fatal("restore included a write after its snapshot")
			}
			before := calls.Load()
			status, tested := curationRequest(t, origin, "POST", "/ai/test", map[string]any{})
			if phase == "retained-key" {
				if status != 200 || tested["ok"] != true || calls.Load() != before+2 {
					t.Fatal("restored encrypted settings did not dispatch with the retained key")
				}
			} else if status != 503 || tested["error"].(map[string]any)["code"] != "SECRET_UNAVAILABLE" || calls.Load() != before {
				t.Fatal("restore dispatched with a wrong or missing installation key")
			}
			if rag {
				status, coverage := curationRequest(t, origin, "GET", "/rag/coverage?projectId="+projectID, nil)
				if status != 200 || coverage["status"] != "complete" || coverage["indexedSources"] != float64(1) {
					t.Fatal("restore lost native passage coverage")
				}
				status, evidence := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "RESTORE_EVIDENCE_WITNESS"})
				if status != 200 || len(evidence["results"].([]any)) != 1 || evidence["results"].([]any)[0].(map[string]any)["recordId"] != record["id"] {
					t.Fatal("restored passage evidence was not retrievable")
				}
				semantic := evidence["semantic"].(map[string]any)
				if semantic["available"] != (phase == "retained-key") {
					t.Fatal("restored retrieval misrepresented provider secret availability")
				}
			}
		})
	}
}
