// Package pins stores the sessions the user pinned to the top of the list.
package pins

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

// Pin identifies a session. Host keeps IDs from different machines apart.
type Pin struct {
	Host string `json:"host"`
	ID   string `json:"id"`
}

type file struct {
	Pins []Pin `json:"pins"`
}

// Store is a JSON file of pins, saved on every change.
// It is safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
	pins []Pin
}

// Load reads path. A missing file is an empty store; an unreadable one is an
// error, so a later save cannot overwrite pins the user still has.
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
	s.pins = f.Pins
	return s, nil
}

// Has reports whether the session is pinned.
func (s *Store) Has(host, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.pins, Pin{host, id})
}

// Toggle pins or unpins the session, saves, and returns whether it is now
// pinned. On a save error the change is undone.
func (s *Store) Toggle(host, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := Pin{host, id}
	old := s.pins
	pinned := !slices.Contains(old, p)
	if pinned {
		s.pins = append(slices.Clone(old), p)
	} else {
		s.pins = slices.DeleteFunc(slices.Clone(old), func(q Pin) bool { return q == p })
	}
	if err := s.save(); err != nil {
		s.pins = old
		return !pinned, err
	}
	return pinned, nil
}

// save writes the file atomically. The caller holds mu.
func (s *Store) save() error {
	data, err := json.MarshalIndent(file{Pins: s.pins}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".pins-*")
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
