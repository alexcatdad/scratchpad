package backend_test

import (
	"archive/tar"
	"bytes"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const retainedRuntimeRevision = "0990411697f9eda7c535acf0d2f0697d7ae6f149"

// historicalAPIURL extracts only the historical web source needed for an API
// rollback test and makes current locked dependencies available without copying
// or modifying the worktree's node_modules.
func historicalAPIURL(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	archive, err := exec.Command("git", "-C", root, "archive", "--format=tar", retainedRuntimeRevision, "apps/web/src").Output()
	if err != nil {
		t.Fatal("pinned historical API source is unavailable; fetch the rollback fixture revision")
	}
	legacyRoot := filepath.Join(t.TempDir(), "legacy")
	srcRoot := filepath.Join(legacyRoot, "apps", "web", "src")
	if err := os.MkdirAll(srcRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	const prefix = "apps/web/src/"
	for {
		header, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal("historical API archive is unreadable")
		}
		if (header.Name == "apps/" || header.Name == "apps/web/" || header.Name == prefix) && header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Name == "pax_global_header" && header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.TrimPrefix(header.Name, prefix)
		if name == header.Name || name == "" || filepath.IsAbs(name) {
			t.Fatalf("historical API archive contains an unexpected path: %q", header.Name)
		}
		clean := filepath.Clean(name)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			t.Fatal("historical API archive path escapes its extraction directory")
		}
		destination := filepath.Join(srcRoot, clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destination, 0o700); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = io.Copy(file, reader); err != nil {
				file.Close()
				t.Fatal(err)
			}
			if err = file.Close(); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("historical API archive contains a non-regular file")
		}
	}
	if err := os.Symlink(filepath.Join(root, "node_modules"), filepath.Join(legacyRoot, "node_modules")); err != nil {
		t.Fatal("npm ci is required for retained TypeScript rollback verification")
	}
	apiPath := filepath.Join(srcRoot, "server", "api.ts")
	if _, err := os.Stat(apiPath); err != nil {
		t.Fatal("pinned historical API module is missing")
	}
	apiURL := url.URL{Scheme: "file", Path: filepath.ToSlash(apiPath)}
	return apiURL.String()
}
