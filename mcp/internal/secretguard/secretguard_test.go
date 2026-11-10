package secretguard

import (
	"encoding/json"
	"strings"
	"testing"
)

// Sample credentials are assembled at run time so that no credential-shaped
// literal is committed to the repository.
func repeat(unit string, n int) string { return strings.Repeat(unit, n) }

var (
	githubToken  = "ghp_" + repeat("a1B2", 9)
	finePAT      = "github_pat_" + repeat("a1B2_", 6)
	awsKeyID     = "AKIA" + repeat("Q7ZP", 4)
	modelKey     = "sk-" + "ant-" + repeat("x9Yz", 8)
	slackToken   = "xoxb-" + repeat("1234", 3) + "-" + repeat("abCD", 4)
	stripeKey    = "sk_" + "live_" + repeat("a1B2", 6)
	googleKey    = "AIza" + repeat("a1B2c", 7)
	npmToken     = "npm_" + repeat("a1B2", 9)
	tailscaleKey = "tskey-" + "auth-" + repeat("k1M2", 6)
	jwt          = "eyJ" + repeat("hbGc", 4) + "." + repeat("eyJz", 4) + "." + repeat("SflK", 4)
	randomValue  = "q7Zp2Lx9Vb4Tn8Wc1Kd6Rf3Hs5Mg0JyEaUoIiNtPBwXhYzAC"
	basicValue   = "dXNlcjp" + "wYXNz"
	basicHeader  = "Basic " + basicValue
	privateKey   = "-----BEGIN OPENSSH " + "PRIVATE KEY-----\n" + repeat("b3BlbnNzaC1rZXk", 4) + "\n-----END OPENSSH " + "PRIVATE KEY-----"
)

func TestTextRedactsKnownCredentialShapes(t *testing.T) {
	cases := []struct {
		name, input, secret, kind, want string
	}{
		{"github token", "pushed with " + githubToken + " today", githubToken, "github-token", "pushed with secret://redacted/github-token today"},
		{"fine-grained token", finePAT, finePAT, "github-token", "secret://redacted/github-token"},
		{"aws key id", "id=" + awsKeyID, awsKeyID, "aws-access-key", "id=secret://redacted/aws-access-key"},
		{"model provider key", "key " + modelKey, modelKey, "api-key", "key secret://redacted/api-key"},
		{"slack token", slackToken, slackToken, "slack-token", "secret://redacted/slack-token"},
		{"stripe key", stripeKey, stripeKey, "stripe-key", "secret://redacted/stripe-key"},
		{"google key", googleKey, googleKey, "google-api-key", "secret://redacted/google-api-key"},
		{"npm token", npmToken, npmToken, "npm-token", "secret://redacted/npm-token"},
		{"tailscale key", tailscaleKey, tailscaleKey, "tailscale-key", "secret://redacted/tailscale-key"},
		{"jwt", "cookie " + jwt, jwt, "jwt", "cookie secret://redacted/jwt"},
		{"private key block", "key:\n" + privateKey + "\ndone", "b3BlbnNzaC1rZXk", "private-key", "key:\nsecret://redacted/private-key\ndone"},
		{"unterminated private key", "-----BEGIN RSA " + "PRIVATE KEY-----\nMIIEow", "MIIEow", "private-key", "secret://redacted/private-key"},
		{"authorization header", `curl -H "Authorization: ` + basicHeader + `"`, basicValue, "authorization-header", `curl -H "Authorization: Basic secret://redacted/authorization-header"`},
		{"bearer token", "sent Bearer abcDEF123456ghiJKL7890 upstream", "abcDEF123456ghiJKL7890", "bearer-token", "sent Bearer secret://redacted/bearer-token upstream"},
		{"url password", "postgres://app:Hunter2pw@db.internal:5432/app", "Hunter2pw", "url-credential", "postgres://app:secret://redacted/url-credential@db.internal:5432/app"},
		{"env assignment", "export DATABASE_PASSWORD=Hunter2hunter2", "Hunter2hunter2", "assigned-secret", "export DATABASE_PASSWORD=secret://redacted/assigned-secret"},
		{"quoted assignment", `api_key: "k3y-Value-99"`, "k3y-Value-99", "assigned-secret", `api_key: "secret://redacted/assigned-secret"`},
		{"camel case name", "clientSecret=Zx9!kLm2pQ", "Zx9!kLm2pQ", "assigned-secret", "clientSecret=secret://redacted/assigned-secret"},
		{"cli flag", "login --password S3cretPass9 --user me", "S3cretPass9", "assigned-secret", "login --password secret://redacted/assigned-secret --user me"},
		{"long random value", "value " + randomValue + " end", randomValue, "high-entropy-value", "value secret://redacted/high-entropy-value end"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var scan Scan
			got := scan.Text(tc.input)
			if got != tc.want {
				t.Fatalf("redacted text = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, tc.secret) {
				t.Fatalf("secret survived in %q", got)
			}
			findings := scan.Findings()
			if len(findings) != 1 || findings[0].Kind != tc.kind || findings[0].Count != 1 {
				t.Fatalf("findings = %#v, want one %s", findings, tc.kind)
			}
		})
	}
}

func TestTextLeavesOrdinaryContentAlone(t *testing.T) {
	inputs := []string{
		"Decided to keep PostgreSQL on the homelab and break it less.",
		"commit 7817b82ccdbd237f960de07a8d4e13d61fd96a6a on main",
		"record rec_a017eba1-8173-4139-99be-8096c74d9c9a in proj_71b90b1c-e906-46e9-b065-4882048bf4f7",
		"fingerprint SHA256:9/b9ebJdhRY6VcmBamdMHcG6OszMSBYlD+zSq0vCZbk",
		"vendorHash = sha256-Qm9vVGhpc0lzQUhhc2hOb3RBU2VjcmV0MTIzNDU2Nzg5MDEy",
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGq7Zp2Lx9Vb4Tn8Wc1Kd6Rf3Hs5Mg0JyEaUoIiNtPB owner",
		"receipt X1DGyV27ZNNwa7tOEYxFTPPXM43qiGN- was rejected",
		"TestDeliveredRetryAfterRestartReturnsReceiptWithoutCachedServerObjects2 passed",
		"codex-offline-capture-acceptance-2026-10-15T00-33-41Z",
		"use secret://provider/main for the token",
		"token: secret://provider/main",
		"password: required",
		"Password: Required.",
		"api_key=YOUR_API_KEY_HERE",
		"token=${GITHUB_TOKEN}",
		"password=<your-password>",
		"private_key=/Users/alex/.ssh/id_ed25519",
		"max_output_tokens: 4096 and input_tokens=182340",
		"token_version=2026.11.07.1",
		"tokenizer=cl100k_base",
		"read --token-file /run/secrets/token",
		"postgres://app:${DB_PASSWORD}@db.internal/app",
		"http://localhost:8080/path@thing",
		"the Authorization header carries a bearer credential",
		"marker SYNTHETIC_CAPTURE_SECRET stays",
	}
	for _, input := range inputs {
		var scan Scan
		if got := scan.Text(input); got != input {
			t.Errorf("changed ordinary text\n input %q\noutput %q", input, got)
		}
		if findings := scan.Findings(); findings != nil {
			t.Errorf("unexpected findings %#v for %q", findings, input)
		}
	}
}

func TestTextIsStableWhenAppliedTwice(t *testing.T) {
	input := "token=" + githubToken + " and postgres://app:Hunter2pw@db/app and " + randomValue
	var first Scan
	once := first.Text(input)
	var second Scan
	if twice := second.Text(once); twice != once {
		t.Fatalf("second pass changed the text: %q -> %q", once, twice)
	}
	if second.Findings() != nil {
		t.Fatalf("second pass reported findings: %#v", second.Findings())
	}
}

func TestJSONRedactsValuesAndPreservesEverythingElse(t *testing.T) {
	raw := []byte("{\n  \"reason\" : \"ran with " + githubToken + "\",\n" +
		`  "count": 12345678901234567890.0010,` + "\n" +
		`  "password": "correcthorse",` + "\n" +
		`  "token": {"value": "kept because the key is not sensitive"},` + "\n" +
		`  "steps": ["export API_KEY=k3y-Value-99", "echo <b>done</b>"],` + "\n" +
		"  \"" + githubToken + "\": true\n}")
	var scan Scan
	got, err := scan.JSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"reason\" : \"ran with secret://redacted/github-token\",\n" +
		`  "count": 12345678901234567890.0010,` + "\n" +
		`  "password": "secret://redacted/assigned-secret",` + "\n" +
		`  "token": {"value": "kept because the key is not sensitive"},` + "\n" +
		`  "steps": ["export API_KEY=secret://redacted/assigned-secret", "echo <b>done</b>"],` + "\n" +
		"  \"" + githubToken + "\": true\n}"
	if string(got) != want {
		t.Fatalf("redacted JSON\n got %s\nwant %s", got, want)
	}
	if !json.Valid(got) {
		t.Fatal("redacted JSON is not valid")
	}
	findings := scan.Findings()
	if len(findings) != 2 || findings[0] != (Finding{Kind: "assigned-secret", Count: 2}) || findings[1] != (Finding{Kind: "github-token", Count: 1}) {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestJSONKeyedValueRules(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"prose under a sensitive key", `{"secret":"stored in the keychain"}`, `{"secret":"stored in the keychain"}`},
		{"reference under a sensitive key", `{"token":"secret://provider/main"}`, `{"token":"secret://provider/main"}`},
		{"number under a sensitive key", `{"token":12345678}`, `{"token":12345678}`},
		{"digits-only string", `{"token":"12345678"}`, `{"token":"12345678"}`},
		{"plural key is not sensitive", `{"tokens":"abcdefgh"}`, `{"tokens":"abcdefgh"}`},
		{"camel case counter is not sensitive", `{"tokenCount":"abcdefgh"}`, `{"tokenCount":"abcdefgh"}`},
		{"camel case secret", `{"clientSecret":"abcdefgh"}`, `{"clientSecret":"secret://redacted/assigned-secret"}`},
		{"snake case with suffix", `{"api_key_value":"abcdefgh"}`, `{"api_key_value":"secret://redacted/assigned-secret"}`},
		{"escaped quote in value", `{"note":"say \"hi\" to ` + githubToken + `"}`, `{"note":"say \"hi\" to secret://redacted/github-token"}`},
		{"unicode escape survives when untouched", `{"note":"café"}`, `{"note":"café"}`},
		{"top-level string", `"` + githubToken + `"`, `"secret://redacted/github-token"`},
		{"array under a sensitive key", `{"password":["abcdefgh"]}`, `{"password":["abcdefgh"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var scan Scan
			got, err := scan.JSON([]byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestJSONRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{``, `{`, `{"a":"b"`, `{"a":"b"} trailing`, `"unterminated`} {
		var scan Scan
		if _, err := scan.JSON([]byte(input)); err == nil {
			t.Errorf("accepted invalid JSON %q", input)
		}
	}
}

func TestJSONReturnsInputUnchangedWhenNothingMatches(t *testing.T) {
	raw := []byte(`{"marker":"SYNTHETIC_CAPTURE_SECRET","n":1.50}`)
	var scan Scan
	got, err := scan.JSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if &got[0] != &raw[0] {
		t.Fatal("unchanged input was copied")
	}
}
