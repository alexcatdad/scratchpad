package backend_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestCredentialRevocationIsIndependentOfExpiredSessionHistory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, browser := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			private, key := githubSyntheticKey(t)
			credential := enrollLocalSSH(t, origin, private, key, browser)
			_, proof := signedChallenge(t, origin, private, key)
			status, login := postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
			if status != 200 {
				t.Fatal("target session fixture failed")
			}
			token := login["accessToken"].(string)
			driver, dsn := "sqlite", path
			if engine == "postgres" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			// Historical operational input: expired rows may predate JSON version/id
			// metadata; they cannot authenticate and must not poison a security action.
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 4352; i++ {
				session := map[string]any{"credentialId": credential, "browser": false, "expiresAt": "2000-01-01T00:00:00Z", "historicalProvenance": map[string]any{"retained": true}}
				if i >= 256 {
					session["id"] = fmt.Sprintf("expired_session_%03d", i)
					session["version"] = 1
				}
				raw, _ := json.Marshal(session)
				if _, err = tx.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('session',$1,$2,1,'2000-01-01T00:00:00Z')`, fmt.Sprintf("expired_session_%03d", i), string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			const unrelated = "synthetic-unrelated-duplicate-session"
			digest := sha256.Sum256([]byte(unrelated))
			sessionID := hex.EncodeToString(digest[:])
			duplicated := fmt.Sprintf("{\"id\":%q,\"version\":1,\"credentialId\":%q,\"credentialId\":\"credential_restored\",\"browser\":false,\"expiresAt\":\"2099-01-01T00:00:00Z\",\"unknownProvenance\":\"retained\"}", sessionID, credential)
			if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('session',$1,$2,1,'2026-10-01T00:00:00Z')`, sessionID, duplicated); err != nil {
				t.Fatal(err)
			}
			status, _ = fetchRecord(t, origin, unrelated)
			if status != 200 {
				t.Fatal("duplicate provenance fixture does not authenticate as its last credential")
			}
			started := time.Now()
			status, _ = credentialRequest(t, origin, "DELETE", "/"+credential, nil, browser)
			if status != 200 {
				t.Fatalf("expired session history prevented credential revocation: HTTP%d", status)
			}
			t.Logf("revoked credential with 4352 expired sessions in %.3f ms", float64(time.Since(started).Microseconds())/1000)
			status, _ = fetchRecord(t, origin, unrelated)
			if status != 200 {
				t.Fatal("session revocation matched earlier duplicate credential provenance")
			}
			status, _ = fetchRecord(t, origin, token)
			if status != 401 {
				t.Fatal("revoked credential retained a live bearer session")
			}
			status, _ = fetchRecord(t, origin, restoredToken)
			if status != 200 {
				t.Fatal("targeted revocation changed unrelated credential sessions")
			}
			status, history := credentialRequest(t, origin, "GET", "", nil, browser)
			if status != 200 {
				t.Fatal("targeted revocation invalidated independent browser authentication")
			}
			retained := false
			for _, value := range history["credentials"].([]any) {
				row := value.(map[string]any)
				if row["id"] == credential && row["source"] == "local" && row["revokedAt"] != nil && row["revokedAt"] != "" {
					retained = true
				}
			}
			if !retained {
				t.Fatal("session invalidation deleted credential revocation provenance")
			}
			status, _ = postJSON(t, origin, "/api/v1/auth/mcp/challenge", map[string]any{"publicKey": key})
			if status != 401 {
				t.Fatal("revoked credential permitted fresh authentication")
			}
		})
	}
}
