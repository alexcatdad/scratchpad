package scratchpad

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type syntheticSDKTransport func(*http.Request) (*http.Response, error)

func (f syntheticSDKTransport) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuthenticatedClientDoesNotFollowRedirects(t *testing.T) {
	for _, tls := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls-%t", tls), func(t *testing.T) {
			forwarded := 0
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded++; w.WriteHeader(200) }))
			defer target.Close()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/api/v1/projects", http.StatusFound)
			})
			var source *httptest.Server
			var transport HttpRequestDoer
			if tls {
				source = httptest.NewTLSServer(handler)
				transport = source.Client()
			} else {
				source = httptest.NewServer(handler)
			}
			defer source.Close()
			client, err := NewAuthenticatedClient(source.URL, Auth{Token: "synthetic-owner-token", HTTPClient: transport})
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.GetProjectsWithResponse(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode() != http.StatusFound || forwarded != 0 {
				t.Fatalf("redirect followed: status=%d calls=%d", response.StatusCode(), forwarded)
			}
		})
	}
}

func TestAuthenticatedTransportOrigins(t *testing.T) {
	for _, baseURL := range []string{"http://remote.example.test", "http://localhost.example.test", "http://127.0.0.1.example.test", "http://localhost@remote.example.test", "http://[::ffff:127.0.0.1]", "http://localhost.", "file:///tmp/synthetic", "http://", "https://synthetic-user:synthetic-password@example.test"} {
		t.Run(baseURL, func(t *testing.T) {
			calls := 0
			transport := syntheticSDKTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"projects":[]}`))}, nil
			})
			_, err := NewAuthenticatedClient(baseURL, Auth{Token: "synthetic-owner-token", HTTPClient: transport})
			if err == nil {
				t.Fatal("unsafe origin accepted")
			}
			if strings.Contains(err.Error(), "synthetic-owner-token") || strings.Contains(err.Error(), "synthetic-password") {
				t.Fatal("unsafe error disclosed credentials")
			}
			if calls != 0 {
				t.Fatal("rejection sent a request")
			}
		})
	}
	for _, baseURL := range []string{"https://remote.example.test", "http://localhost:3000", "http://127.0.0.1:3000", "http://[::1]:3000", "http://LOCALHOST:3000", "http://[0:0:0:0:0:0:0:1]:3000"} {
		t.Run(baseURL, func(t *testing.T) {
			calls := 0
			transport := syntheticSDKTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != "Bearer synthetic-owner-token" {
					t.Error("missing auth")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"projects":[]}`))}, nil
			})
			client, err := NewAuthenticatedClient(baseURL, Auth{Token: "synthetic-owner-token", HTTPClient: transport})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = client.GetProjectsWithResponse(context.Background()); err != nil || calls != 1 {
				t.Fatalf("accepted origin request: %d %v", calls, err)
			}
		})
	}
}

func TestSSHChallengePreservesExpiryLexeme(t *testing.T) {
	const expiry = "2026-10-09T12:00:00.120Z"
	response := func(purpose string) *http.Response {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"version":2,"recipient":"https://memory.example.test","purpose":"` + purpose + `","challengeId":"synthetic","nonce":"synthetic","namespace":"scratchpad-auth-v2","expiresAt":"` + expiry + `"}`))}
	}
	login, err := ParsePostAuthMcpChallengeResponse(response("ssh_login"))
	if err != nil || login.JSON200 == nil || login.JSON200.ExpiresAt != expiry {
		t.Fatalf("login expiry lexeme: %#v %v", login, err)
	}
	enroll, err := ParsePostAuthCredentialsChallengeResponse(response("ssh_enroll"))
	if err != nil || enroll.JSON200 == nil || enroll.JSON200.ExpiresAt != expiry {
		t.Fatalf("enrollment expiry lexeme: %#v %v", enroll, err)
	}
}

// The Node integration harness serves the actual API/auth/database, not canned responses.
func TestAuthenticatedHTTP(t *testing.T) {
	base := os.Getenv("SCRATCHPAD_SDK_TEST_URL")
	if base == "" {
		t.Skip("run npm run sdk:test for the real API fixture")
	}
	client, err := NewAuthenticatedClient(base, Auth{Token: os.Getenv("SCRATCHPAD_SDK_TEST_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	projects, err := client.GetProjectsWithResponse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if projects.StatusCode() != 200 || projects.JSON200 == nil || projects.JSON200.Projects == nil || len(*projects.JSON200.Projects) != 1 {
		t.Fatalf("projects: %s", projects.Body)
	}
	var body PostProjectsResolveJSONRequestBody
	body.Context.Git.Remote = "https://github.com/example/go-sdk-fixture.git"
	resolved, err := client.PostProjectsResolveWithResponse(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.StatusCode() != 200 || resolved.JSON200 == nil || resolved.JSON200.Project == nil {
		t.Fatalf("resolve: %s", resolved.Body)
	}
	missing, err := client.GetRecordsIdWithResponse(ctx, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if missing.StatusCode() != 404 || missing.JSONDefault == nil || missing.JSONDefault.Error.Code != "RECORD_NOT_FOUND" {
		t.Fatalf("missing: %s", missing.Body)
	}
	guest, err := NewAuthenticatedClient(base, Auth{})
	if err != nil {
		t.Fatal(err)
	}
	denied, err := guest.GetProjectsWithResponse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if denied.StatusCode() != 401 || denied.JSONDefault == nil || denied.JSONDefault.Error.Code != "AUTH_REQUIRED" {
		t.Fatalf("unauthorized: %s", denied.Body)
	}
}
