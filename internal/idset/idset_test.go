package idset

import (
	"os"
	"path/filepath"
	"testing"
)

func TestToggleAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asm", "pins.json")
	s, err := Load(path, "pins")
	if err != nil {
		t.Fatal(err)
	}
	if added, err := s.Toggle("local", "a"); err != nil || !added {
		t.Fatalf("add a = %v, %v", added, err)
	}
	if added, err := s.Toggle("local", "b"); err != nil || !added {
		t.Fatalf("add b = %v, %v", added, err)
	}
	if added, err := s.Toggle("local", "a"); err != nil || added {
		t.Fatalf("remove a = %v, %v", added, err)
	}

	r, err := Load(path, "pins")
	if err != nil {
		t.Fatal(err)
	}
	if r.Has("local", "a") || !r.Has("local", "b") || r.Has("srv1", "b") {
		t.Errorf("reloaded set = %+v", r.ids)
	}
}

// TestReadsExistingFormat keeps files written by earlier versions readable.
func TestReadsExistingFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")
	data := `{"pins": [{"host": "local", "id": "61f91f49"}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path, "pins")
	if err != nil || !s.Has("local", "61f91f49") {
		t.Errorf("Load = %+v, %v", s, err)
	}
}

func TestLoadRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, "pins"); err == nil {
		t.Error("corrupt file loaded without error")
	}
}
