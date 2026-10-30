package main

import "testing"

func TestServerOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"https://memory.example.com":   "https://memory.example.com",
		"https://memory.example.com/":  "https://memory.example.com",
		" http://localhost:3000 ":      "http://localhost:3000",
		"https://memory.example.com/x": "",
		"http://memory.example.com":    "",
		"https://u:p@memory.example":   "",
		"https://memory.example.com?q": "",
	} {
		got, err := serverOrigin(raw)
		if want == "" && err == nil || want != "" && got != want {
			t.Errorf("serverOrigin(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
}

func TestParsePublicKeyMatchesSSHKeygenFingerprint(t *testing.T) {
	// Synthetic key; the fingerprint is ssh-keygen -lf output for it.
	key, ok := parsePublicKey("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPbwKrAbNCha8jtQRbH/kNjbGYDULmDb22vBQyA7QFjc synthetic", "test", "")
	if !ok || key.publicKey != "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPbwKrAbNCha8jtQRbH/kNjbGYDULmDb22vBQyA7QFjc" ||
		key.fingerprint != "SHA256:BWiHFCV+rCVKT44JFq8CVGHaISQq6y8llWar2Ig2ARk" {
		t.Fatalf("parsePublicKey = %+v, %v", key, ok)
	}
	if _, ok := parsePublicKey("not a key", "test", ""); ok {
		t.Fatal("accepted invalid key")
	}
}
