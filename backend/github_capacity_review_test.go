package backend_test

import (
	"database/sql"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestBlockedGithubCallbacksDoNotConsumeIndependentProofCapacity(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			private, key := githubSyntheticKey(t)
			fixture := &githubFixture{key: key}
			entered, release := make(chan struct{}, 2), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			githubService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/user" {
					select {
					case entered <- struct{}{}:
					case <-r.Context().Done():
						return
					}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				fixture.ServeHTTP(w, r)
			}))
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			statement := `UPDATE entities SET data=json_set(data,'$.publicKey',$1) WHERE kind='credential' AND id='credential_restored'`
			if engine == "postgres" {
				statement = `UPDATE entities SET data=jsonb_set(data,'{publicKey}',to_jsonb($1::text)) WHERE kind='credential' AND id='credential_restored'`
			}
			if _, err = db.Exec(statement, key); err != nil {
				t.Fatal(err)
			}
			origin := startServer(t, path, databaseURL)
			client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			requests := []*http.Request{}
			for range 3 {
				target, cookie := githubOptions(t, origin, "link", "", local)
				req, _ := http.NewRequest("GET", origin+"/api/v1/auth/github/callback?code=synthetic-code&state="+target.Query().Get("state"), nil)
				req.AddCookie(cookie)
				requests = append(requests, req)
			}
			done := make(chan int, 2)
			remaining := 2
			defer func() {
				unblock()
				for remaining > 0 {
					select {
					case <-done:
						remaining--
					case <-time.After(9 * time.Second):
						t.Error("owned callback did not terminate")
						return
					}
				}
			}()
			for _, req := range requests[:2] {
				go func(req *http.Request) {
					res, err := client.Do(req)
					if err != nil {
						done <- 0
						return
					}
					io.Copy(io.Discard, res.Body)
					res.Body.Close()
					done <- res.StatusCode
				}(req)
			}
			for range 2 {
				select {
				case <-entered:
				case <-time.After(4 * time.Second):
					t.Fatal("callbacks did not reach blocked external boundary")
				}
			}
			res, err := client.Do(requests[2])
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 429 {
				t.Fatal("OAuth concurrency was not bounded")
			}
			t.Run("invalid-state", func(t *testing.T) {
				res, err := client.Get(origin + "/api/v1/auth/github/callback?code=synthetic-code&state=invalid-but-long-synthetic-state")
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if res.StatusCode != 303 {
					t.Fatal("unbound callback claimed verification capacity before state validation")
				}
			})
			t.Run("independent-proof", func(t *testing.T) {
				_, proof := signedChallenge(t, origin, private, key)
				status, response := postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
				if status != 200 || response["accessToken"] == nil {
					t.Fatalf("blocked OAuth exhausted independent proof admission: HTTP%d", status)
				}
			})
			unblock()
			for remaining > 0 {
				select {
				case status := <-done:
					remaining--
					if status != 302 && status != 303 {
						t.Errorf("released callback failed HTTP%d", status)
					}
				case <-time.After(4 * time.Second):
					t.Fatal("callback remained blocked")
				}
			}
		})
	}
}
