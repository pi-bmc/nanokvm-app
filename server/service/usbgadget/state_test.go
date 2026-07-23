package usbgadget

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")

	want := State{Ethernet: EthernetNCM, Disk: false}
	if err := saveState(path, want); err != nil {
		t.Fatalf("saveState: %v", err)
	}
	got, ok := loadState(path)
	if !ok {
		t.Fatal("loadState: ok=false after save")
	}
	if got != want {
		t.Fatalf("loadState = %+v, want %+v", got, want)
	}
}

func TestLoadStateAbsentIsSentinel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	got, ok := loadState(path)
	if ok {
		t.Fatal("loadState: ok=true for absent file (must be the first-run sentinel)")
	}
	if got != defaultState() {
		t.Fatalf("loadState default = %+v, want %+v", got, defaultState())
	}
}

func TestLoadStateEmptyPath(t *testing.T) {
	if _, ok := loadState(""); ok {
		t.Fatal("loadState(\"\") should be ok=false")
	}
}

func TestLoadStateCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadState(path); ok {
		t.Fatal("loadState should report ok=false on corrupt JSON")
	}
}

func TestLoadStateNormalizesEmptyEthernet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"disk":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := loadState(path)
	if !ok {
		t.Fatal("loadState: ok=false")
	}
	if got.Ethernet != EthernetOff {
		t.Fatalf("empty ethernet not normalized: got %q, want %q", got.Ethernet, EthernetOff)
	}
}
