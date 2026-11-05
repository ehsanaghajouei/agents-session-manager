// Package names stores the names the user gave sessions in asm. Claude's own
// files are never changed; a name is shown only in asm.
package names

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// Name is the name of one session. Host keeps IDs from different machines apart.
type Name struct {
	Host string `json:"host"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type file struct {
	Names []Name `json:"names"`
}

// Store is a JSON file of names, saved on every change.
// It is safe for concurrent use.
type Store struct {
	path  string
	mu    sync.Mutex
	names []Name
}

// Load reads path. A missing file is an empty store; an unreadable one is an
// error, so a later save cannot overwrite names the user still has.
func Load(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.names = f.Names
	return s, nil
}

// Get returns the session's name, or "" when it has none.
func (s *Store) Get(host, id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.index(host, id); i >= 0 {
		return s.names[i].Name
	}
	return ""
}

// Set names the session and saves. An empty name removes it.
// On a save error the change is undone.
func (s *Store) Set(host, id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.names
	s.names = slices.Clone(old)
	switch i := s.index(host, id); {
	case name == "" && i >= 0:
		s.names = slices.Delete(s.names, i, i+1)
	case name == "":
		s.names = old
		return nil
	case i >= 0:
		s.names[i].Name = name
	default:
		s.names = append(s.names, Name{host, id, name})
	}
	if err := s.save(); err != nil {
		s.names = old
		return err
	}
	return nil
}

// index returns the position of the session's name, or -1. The caller holds mu.
func (s *Store) index(host, id string) int {
	return slices.IndexFunc(s.names, func(n Name) bool { return n.Host == host && n.ID == id })
}

// save writes the file atomically. The caller holds mu.
func (s *Store) save() error {
	data, err := json.MarshalIndent(file{Names: s.names}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".names-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
