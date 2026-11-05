// Package watch turns file changes under Claude's data directory into a
// debounced "something changed" signal.
package watch

import (
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher signals on C at most once per debounce interval.
type Watcher struct {
	C  <-chan struct{}
	w  *fsnotify.Watcher
	c  chan struct{}
	ds []string // dirs whose new subdirectories are watched too
}

// New watches each dir in flat, and each dir in nested plus its immediate
// subdirectories. Missing dirs are skipped.
func New(debounce time.Duration, flat, nested []string) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	c := make(chan struct{}, 1)
	w := &Watcher{C: c, w: fw, c: c, ds: nested}
	for _, d := range flat {
		_ = fw.Add(d)
	}
	for _, d := range nested {
		if fw.Add(d) != nil {
			continue
		}
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if e.IsDir() {
				_ = fw.Add(filepath.Join(d, e.Name()))
			}
		}
	}
	go w.loop(debounce)
	return w, nil
}

func (w *Watcher) loop(debounce time.Duration) {
	var timer <-chan time.Time
	for {
		select {
		case ev, ok := <-w.w.Events:
			if !ok {
				close(w.c)
				return
			}
			if ev.Has(fsnotify.Create) && w.isNested(filepath.Dir(ev.Name)) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					_ = w.w.Add(ev.Name)
				}
			}
			if timer == nil {
				timer = time.After(debounce)
			}
		case <-timer:
			timer = nil
			select {
			case w.c <- struct{}{}:
			default: // a signal is already pending
			}
		case _, ok := <-w.w.Errors:
			if !ok {
				close(w.c)
				return
			}
		}
	}
}

func (w *Watcher) isNested(dir string) bool {
	for _, d := range w.ds {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

// Close stops watching and closes C.
func (w *Watcher) Close() error { return w.w.Close() }
