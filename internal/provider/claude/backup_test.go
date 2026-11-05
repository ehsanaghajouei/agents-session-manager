package claude

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExportImport(t *testing.T) {
	ctx := context.Background()
	ex := host.Local{}

	// System A. The session is still writing a line, which must be left out.
	srcDir, path := setup(t, fixture())
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"user","cwd":"/tmp/proj","partial`)
	f.Close()
	hist := filepath.Join(srcDir, "file-history", sid)
	if err := os.MkdirAll(hist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hist, "abc@v1"), []byte("old contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(srcDir, "claude-not-installed", nil, nil)
	sessions, err := a.Discover(ctx, ex)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Discover = %v, %v", sessions, err)
	}
	backup := filepath.Join(t.TempDir(), "backups", sid+".tar.gz")
	if err := a.Export(ctx, ex, sessions[0], backup); err != nil {
		t.Fatal(err)
	}

	// System B, where the project lives at another path.
	dstDir := t.TempDir()
	b := New(dstDir, "claude-not-installed", nil, nil)
	info, err := b.Inspect(ctx, ex, backup)
	if err != nil {
		t.Fatal(err)
	}
	if info.SessionID != sid || info.CWD != "/tmp/proj" || info.Title != "Fix login bug" || info.Version != "2.1.300" || info.Exists {
		t.Errorf("Inspect = %+v", info)
	}
	if err := b.Import(ctx, ex, backup, "/srv/proj", false); err != nil {
		t.Fatal(err)
	}

	proj := filepath.Join(dstDir, "projects", "-srv-proj")
	want := strings.ReplaceAll(strings.Join(fixture(), "\n")+"\n", `"cwd":"/tmp/proj"`, `"cwd":"/srv/proj"`)
	if got := readFile(t, filepath.Join(proj, sid+".jsonl")); got != want {
		t.Error("imported transcript differs from the original with cwd moved")
	}
	if got := readFile(t, filepath.Join(proj, sid, "subagents", "agent-1.jsonl")); !strings.Contains(got, `"cwd":"/srv/proj"`) {
		t.Errorf("subagent transcript = %q", got)
	}
	if got := readFile(t, filepath.Join(dstDir, "file-history", sid, "abc@v1")); got != "old contents" {
		t.Errorf("file history = %q", got)
	}
	if entries, _ := os.ReadDir(dstDir); len(entries) != 2 {
		t.Errorf("claude dir holds %v, want only projects and file-history", entries)
	}

	if err := b.Import(ctx, ex, backup, "/srv/proj", false); !errors.Is(err, provider.ErrSessionExists) {
		t.Errorf("second import = %v, want ErrSessionExists", err)
	}
	if info, err := b.Inspect(ctx, ex, backup); err != nil || !info.Exists {
		t.Errorf("Inspect after import = %+v, %v", info, err)
	}
	// Replacing moves the session; no second copy is left behind, since
	// claude --resume refuses an ID found in two projects.
	if err := b.Import(ctx, ex, backup, "/srv/other", true); err != nil {
		t.Fatal(err)
	}
	if copies, _ := b.copies(sid); len(copies) != 1 || !strings.Contains(copies[0], "-srv-other") {
		t.Errorf("copies after replace = %v", copies)
	}
	if _, err := os.Stat(filepath.Join(proj, sid)); !os.IsNotExist(err) {
		t.Error("the replaced copy's folder is still there")
	}
}

func TestImportRejectsUnexpectedEntries(t *testing.T) {
	m, _ := json.Marshal(manifest{Format: backupFormat, Backup: provider.Backup{Agent: "claude", SessionID: sid, CWD: "/tmp/proj"}})
	for _, entry := range []string{"../escape", "transcript/../../escape", "/abs", "other.txt"} {
		path := filepath.Join(t.TempDir(), "bad.tar.gz")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(f)
		tw := tar.NewWriter(gz)
		for _, e := range []struct{ name, body string }{{manifestName, string(m)}, {transcriptName, "{}\n"}, {entry, "x"}} {
			tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: e.name, Mode: 0o600, Size: int64(len(e.body))})
			tw.Write([]byte(e.body))
		}
		tw.Close()
		gz.Close()
		f.Close()

		dir := t.TempDir()
		err = New(dir, "claude", nil, nil).Import(context.Background(), host.Local{}, path, "/tmp/proj", false)
		if err == nil || !strings.Contains(err.Error(), "unexpected entry") {
			t.Errorf("%s: Import = %v", entry, err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%s: claude dir holds %v after a failed import", entry, entries)
		}
	}
}

func TestCWDRewriter(t *testing.T) {
	rw := cwdRewriter("/a/b", "/c")
	for in, want := range map[string]string{
		`{"cwd":"/a/b","x":1}`:      `{"cwd":"/c","x":1}`,
		`{"cwd":"/a/b/sub"}`:        `{"cwd":"/c/sub"}`,
		`{"cwd":"/a/bc"}`:           `{"cwd":"/a/bc"}`,
		`{"t":"\"cwd\":\"/a/b\""}`:  `{"t":"\"cwd\":\"/a/b\""}`,
		`{"file_path":"/a/b/x.go"}`: `{"file_path":"/a/b/x.go"}`,
	} {
		if got := string(rw([]byte(in))); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
	if cwdRewriter("/a", "/a") != nil {
		t.Error("rewriter for an unchanged path")
	}
}

func TestProjectDirName(t *testing.T) {
	if got := projectDirName("/home/u/my_proj.v2"); got != "-home-u-my-proj-v2" {
		t.Errorf("got %q", got)
	}
}
