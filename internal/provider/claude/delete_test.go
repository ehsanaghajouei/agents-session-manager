package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
)

func writeDeleteFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteSessionFiles(t *testing.T) {
	dir, transcript := setup(t, fixture())
	p := New(dir, "", nil, nil)
	session := discoverOne(t, p)
	sessionDir := filepath.Join(filepath.Dir(transcript), sid)
	history := filepath.Join(dir, historyDir, sid)
	writeDeleteFixture(t, filepath.Join(history, "checkpoint"), "session data")
	shared := filepath.Join(dir, "history.jsonl")
	other := filepath.Join(filepath.Dir(transcript), "other.jsonl")
	record := filepath.Join(dir, "sessions", "42.json")
	writeDeleteFixture(t, shared, "shared data")
	writeDeleteFixture(t, other, "another session")
	writeDeleteFixture(t, record, `{"pid":42,"sessionId":"`+sid+`","procStart":"old"}`)
	if err := os.Symlink(shared, filepath.Join(sessionDir, "link")); err != nil {
		t.Fatal(err)
	}
	err := p.deleteSession(context.Background(), session, func(pid int, start string) (bool, error) {
		if pid != 42 || start != "old" {
			t.Fatalf("wrong process checked: %d %q", pid, start)
		}
		return false, nil // A stale PID record must not block deletion.
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{transcript, sessionDir, history} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was not removed: %v", path, err)
		}
	}
	for _, path := range []string{shared, other, record} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("unrelated file %s was affected: %v", path, err)
		}
	}
}

func TestDeleteRechecksClaudeActivity(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		live          bool
		checkErr      error
	}{
		{name: "started after scan", message: "stop the running Claude process", live: true},
		{name: "activity unavailable", message: "cannot verify Claude activity", checkErr: errors.New("process lookup failed")},
		{name: "malformed activity record", message: "cannot verify Claude activity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, transcript := setup(t, fixture())
			p := New(dir, "", nil, nil)
			session := discoverOne(t, p)
			record := `{"pid":42,"sessionId":"` + sid + `","procStart":"start"}`
			if tc.name == "malformed activity record" {
				record = "{"
			}
			writeDeleteFixture(t, filepath.Join(dir, "sessions", "42.json"), record)
			err := p.deleteSession(context.Background(), session, func(int, string) (bool, error) {
				return tc.live, tc.checkErr
			})
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("Delete = %v, want %q", err, tc.message)
			}
			if _, err := os.Stat(transcript); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeleteRejectsUnsafePaths(t *testing.T) {
	t.Run("duplicate ID", func(t *testing.T) {
		dir, transcript := setup(t, fixture())
		p := New(dir, "", nil, nil)
		session := discoverOne(t, p)
		copy := filepath.Join(p.ProjectsDir(), "-other", filepath.Base(transcript))
		writeDeleteFixture(t, copy, "other transcript")
		if err := p.Delete(context.Background(), host.Local{}, session); err == nil || !strings.Contains(err.Error(), "more than one project") {
			t.Fatalf("Delete = %v, want duplicate ID error", err)
		}
		for _, path := range []string{transcript, copy} {
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("symlinked history", func(t *testing.T) {
		dir, transcript := setup(t, fixture())
		p := New(dir, "", nil, nil)
		session := discoverOne(t, p)
		outside := t.TempDir()
		checkpoint := filepath.Join(outside, sid, "checkpoint")
		writeDeleteFixture(t, checkpoint, "outside data")
		if err := os.Symlink(outside, filepath.Join(dir, historyDir)); err != nil {
			t.Fatal(err)
		}
		if err := p.Delete(context.Background(), host.Local{}, session); err == nil {
			t.Fatal("Delete accepted symlinked history")
		}
		for _, path := range []string{transcript, checkpoint} {
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
		}
	})
}
