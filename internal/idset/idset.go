// Package idset stores a set of sessions in a JSON file, such as the pinned
// or the hidden ones.
package idset

import (
	"slices"
	"sync"

	"github.com/ehsanaghajouei/agents-session-manager/internal/jsonfile"
)

// ID identifies a session. Host keeps IDs from different machines apart.
type ID struct {
	Host string `json:"host"`
	ID   string `json:"id"`
}

// Set is the list stored under one key of a JSON file, saved on every change.
// It is safe for concurrent use.
type Set struct {
	path, key string
	mu        sync.Mutex
	ids       []ID
}

// Load reads the list stored under key in path. A missing file is an empty
// set; an unreadable one is an error, so a later save cannot overwrite
// entries the user still has.
func Load(path, key string) (*Set, error) {
	f := map[string][]ID{}
	if err := jsonfile.Read(path, &f); err != nil {
		return nil, err
	}
	return &Set{path: path, key: key, ids: f[key]}, nil
}

// Has reports whether the session is in the set.
func (s *Set) Has(host, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.ids, ID{host, id})
}

// Toggle adds or removes the session, saves, and returns whether it is now in
// the set. On a save error the change is undone.
func (s *Set) Toggle(host, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x := ID{host, id}
	old := s.ids
	added := !slices.Contains(old, x)
	if added {
		s.ids = append(slices.Clone(old), x)
	} else {
		s.ids = slices.DeleteFunc(slices.Clone(old), func(y ID) bool { return y == x })
	}
	if err := jsonfile.Write(s.path, map[string][]ID{s.key: s.ids}); err != nil {
		s.ids = old
		return !added, err
	}
	return added, nil
}
