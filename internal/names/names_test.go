package names

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asm", "names.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []Name{{"local", "a", "first"}, {"local", "b", "bee"}, {"local", "a", "renamed"}, {"local", "b", ""}} {
		if err := s.Set(n.Host, n.ID, n.Name); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Set("local", "c", ""); err != nil { // clearing a missing name is a no-op
		t.Fatal(err)
	}

	r, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Get("local", "a"); got != "renamed" {
		t.Errorf("a = %q", got)
	}
	if got := r.Get("local", "b"); got != "" {
		t.Errorf("b = %q, want cleared", got)
	}
	if got := r.Get("srv1", "a"); got != "" {
		t.Errorf("srv1/a = %q, want none", got)
	}
	if len(r.names) != 1 {
		t.Errorf("stored names = %+v", r.names)
	}
}

func TestLoadRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("corrupt file loaded without error")
	}
}
