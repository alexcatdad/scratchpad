package scratchpad

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

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
