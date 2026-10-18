package localcapture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradeStateRoundTripAndPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "upgrade-in-progress.json")
	state := UpgradeState{
		Version: upgradeStateVersion, ServicePath: "/Applications/Scratchpad.app/Contents/Helpers/scratchpad-service",
		SocketPath: "/tmp/scratchpad-test/service.sock", DatabasePath: "/tmp/scratchpad-test/captures.sqlite",
		KeyIdentifier: "com.alexcatdad.scratchpad.capture-queue.v1", Restart: true,
	}
	if err := writeUpgradeState(path, state); err != nil {
		t.Fatal(err)
	}
	got, err := readUpgradeState(path)
	if err != nil || got != state {
		t.Fatalf("readUpgradeState() = %#v, %v; want %#v", got, err, state)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("marker mode = %v, %v; want 0600", info, err)
	}
	if err = writeUpgradeState(path, state); err == nil {
		t.Fatal("second upgrade attempt replaced the existing marker")
	}
	if err = removeUpgradeState(path); err != nil {
		t.Fatal(err)
	}
	if _, err = readUpgradeState(path); !os.IsNotExist(err) {
		t.Fatalf("read after removal error = %v, want not-exist", err)
	}
}

func TestUpgradeStateRejectsUnsafeAndMalformedMarkers(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "upgrade.json")
	if err := os.WriteFile(marker, []byte(`{"version":1,"servicePath":"/service","socketPath":"/socket","databasePath":"/db","keyIdentifier":"key","restart":true,"extra":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readUpgradeState(marker); err == nil {
		t.Fatal("unknown marker field was accepted")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", marker); err != nil {
		t.Fatal(err)
	}
	if _, err := readUpgradeState(marker); err == nil {
		t.Fatal("symlink marker was accepted")
	}
}
