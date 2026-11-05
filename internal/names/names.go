// Package names stores the names the user gave sessions in asm. Claude's own
// files are never changed; a name is shown only in asm.
package names

import (
	"slices"
	"sync"

	"github.com/ehsanaghajouei/agents-session-manager/internal/jsonfile"
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
	var f file
	if err := jsonfile.Read(path, &f); err != nil {
		return nil, err
	}
	return &Store{path: path, names: f.Names}, nil
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

// save writes the file. The caller holds mu.
func (s *Store) save() error {
	return jsonfile.Write(s.path, file{Names: s.names})
}
