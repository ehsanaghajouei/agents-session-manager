package pins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestToggleAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asm", "pins.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if pinned, err := s.Toggle("local", "a"); err != nil || !pinned {
		t.Fatalf("pin a = %v, %v", pinned, err)
	}
	if pinned, err := s.Toggle("local", "b"); err != nil || !pinned {
		t.Fatalf("pin b = %v, %v", pinned, err)
	}
	if pinned, err := s.Toggle("local", "a"); err != nil || pinned {
		t.Fatalf("unpin a = %v, %v", pinned, err)
	}

	r, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Has("local", "a") || !r.Has("local", "b") || r.Has("srv1", "b") {
		t.Errorf("reloaded pins = %+v", r.pins)
	}
}

func TestLoadRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("corrupt file loaded without error")
	}
}
