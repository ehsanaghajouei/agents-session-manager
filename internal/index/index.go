// Package index caches parsed transcript metadata on disk so unchanged files
// are not re-read, and appended files are read only from where parsing stopped.
package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// version changes when the cached data layout changes; old caches are dropped.
const version = 1

// Entry describes one cached file.
type Entry struct {
	Size    int64           `json:"size"`
	ModTime int64           `json:"mtime"`  // UnixNano
	Offset  int64           `json:"offset"` // bytes parsed so far
	Data    json.RawMessage `json:"data"`
}

type file struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// Store is a JSON file of entries keyed by an opaque string.
// It is safe for concurrent use.
type Store struct {
	path    string
	mu      sync.Mutex
	entries map[string]Entry
	dirty   bool
}

// Open loads the store at path. A missing or unreadable cache starts empty.
func Open(path string) *Store {
	s := &Store{path: path, entries: map[string]Entry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var f file
	if json.Unmarshal(data, &f) == nil && f.Version == version && f.Entries != nil {
		s.entries = f.Entries
	}
	return s
}

func (s *Store) Get(key string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	return e, ok
}

func (s *Store) Put(key string, e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = e
	s.dirty = true
}

// Prune deletes entries whose key starts with prefix and is not in keep.
func (s *Store) Prune(prefix string, keep map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.entries {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix && !keep[k] {
			delete(s.entries, k)
			s.dirty = true
		}
	}
}

// Save writes the store if it changed. The write is atomic.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty || s.path == "" {
		return nil
	}
	data, err := json.Marshal(file{Version: version, Entries: s.entries})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".index-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return err
	}
	s.dirty = false
	return nil
}
