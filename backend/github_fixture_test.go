package backend_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The subprocess still calls the fixed public GitHub HTTPS origins. Only this
// external service boundary is replaced, through a trusted synthetic TLS proxy.
func githubService(t *testing.T, handler http.Handler) {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic GitHub fixture"}, DNSNames: []string{"github.com", "api.github.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &private.PublicKey, private)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || (r.Host != "github.com:443" && r.Host != "api.github.com:443") {
			http.Error(w, "unexpected destination", 403)
			return
		}
		upstream, err := net.Dial("tcp", server.Listener.Addr().String())
		if err != nil {
			http.Error(w, "fixture unavailable", 503)
			return
		}
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buffer.Flush()
		go func() { defer client.Close(); defer upstream.Close(); io.Copy(upstream, client) }()
		go func() { defer client.Close(); defer upstream.Close(); io.Copy(client, upstream) }()
	}))
	t.Cleanup(proxy.Close)
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("SSL_CERT_FILE", path)
	t.Setenv("SSL_CERT_DIR", dir)
	t.Setenv("SCRATCHPAD_GITHUB_CLIENT_ID", "synthetic-client")
	t.Setenv("SCRATCHPAD_GITHUB_CLIENT_SECRET", "synthetic-secret")
}
