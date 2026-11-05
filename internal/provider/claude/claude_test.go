package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/index"
	"github.com/ehsanaghajouei/agents-session-manager/internal/proc"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

const sid = "11111111-2222-3333-4444-555555555555"

// msg builds a user/assistant record. extra is spliced in as raw JSON fields.
func msg(typ, ts, content, extra string) string {
	role := typ
	model := ""
	if typ == "assistant" {
		model = `"model":"claude-opus-5-5",`
	}
	branch := "main"
	if strings.Contains(extra, `"gitBranch"`) {
		branch = ""
	}
	b := ""
	if branch != "" {
		b = fmt.Sprintf(`"gitBranch":%q,`, branch)
	}
	return fmt.Sprintf(`{"parentUuid":null,"isSidechain":false,%s"type":%q,"message":{%s"role":%q,"content":%s},"timestamp":%q,"cwd":"/tmp/proj",%s"version":"2.1.300","sessionId":%q}`,
		extra, typ, model, role, content, ts, b, sid)
}

func fixture() []string {
	return []string{
		`{"type":"mode","mode":"normal","sessionId":"` + sid + `"}`,
		msg("user", "2026-10-01T10:00:00.000Z", `"Fix the login bug"`, ""),
		`{"type":"ai-title","aiTitle":"Fix login bug","sessionId":"` + sid + `"}`,
		msg("assistant", "2026-10-01T10:00:05.000Z", `[{"type":"thinking","thinking":"","signature":"x"}]`, ""),
		msg("assistant", "2026-10-01T10:00:06.000Z", `[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/tmp/proj/login.go"}}]`, ""),
		msg("user", "2026-10-01T10:00:07.000Z", `[{"tool_use_id":"t1","type":"tool_result","content":"package main"}]`, ""),
		msg("assistant", "2026-10-01T10:00:08.000Z", `[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./..."}}]`, ""),
		`{"type":"api-request-blob","hash":"h","message":{"role":"user","content":"` + strings.Repeat("x", 300_000) + `"},"sessionId":"` + sid + `"}`,
		msg("assistant", "2026-10-01T10:00:10.000Z", `[{"type":"text","text":"Fixed the **bug**."}]`, ""),
		msg("user", "2026-10-01T10:01:00.000Z", `"<local-command-caveat>Caveat</local-command-caveat>"`, `"isMeta":true,`),
		msg("user", "2026-10-01T10:02:00.000Z", `"<command-name>/compact</command-name>\n<command-message>compact</command-message>\n<command-args>keep tests</command-args>"`, ""),
		msg("user", "2026-10-01T10:03:00.000Z", `[{"type":"text","text":"[Request interrupted by user]"}]`, ""),
		strings.Replace(msg("user", "2026-10-01T10:04:00.000Z", `"subagent prompt"`, ""), `"isSidechain":false`, `"isSidechain":true`, 1),
		msg("user", "2026-10-01T10:05:00.000Z", `"Now add a test"`, `"gitBranch":"fix-login",`),
		`{"type":"last-prompt","lastPrompt":"Now add a test","sessionId":"` + sid + `"}`,
		`{"type":"cost-state","sessionId":"` + sid + `","totalCostUSD":1.25,"totalLinesAdded":10,"totalLinesRemoved":2,"modelUsage":{"m":{"inputTokens":100,"outputTokens":50,"cacheReadInputTokens":1000,"cacheCreationInputTokens":200,"costUSD":1.25}}}`,
	}
}

// setup writes lines as a transcript and returns the claude dir and file path.
func setup(t *testing.T, lines []string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	proj := filepath.Join(dir, "projects", "-tmp-proj")
	if err := os.MkdirAll(filepath.Join(proj, sid, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A subagent transcript must not show up as a session.
	sub := filepath.Join(proj, sid, "subagents", "agent-1.jsonl")
	if err := os.WriteFile(sub, []byte(lines[1]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, sid+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func discoverOne(t *testing.T, p *Provider) provider.Session {
	t.Helper()
	sessions, err := p.Discover(context.Background(), host.Local{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	return sessions[0]
}

func TestDiscoverSummary(t *testing.T) {
	dir, path := setup(t, fixture())
	s := discoverOne(t, New(dir, "claude", nil, nil))

	want := provider.Session{
		Provider:     "claude",
		Host:         "local",
		ID:           sid,
		Path:         path,
		Title:        "Fix login bug",
		FirstPrompt:  "Fix the login bug",
		LastPrompt:   "Now add a test",
		CWD:          "/tmp/proj",
		GitBranch:    "fix-login",
		Model:        "claude-opus-5-5",
		Version:      "2.1.300",
		Started:      time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC),
		LastActive:   time.Date(2026, 10, 1, 10, 5, 0, 0, time.UTC),
		Messages:     10, // sidechain record excluded
		Prompts:      2,  // command, interruption, meta and tool results excluded
		CostUSD:      1.25,
		HasCost:      true,
		Tokens:       1350,
		LinesAdded:   10,
		LinesRemoved: 2,
	}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("summary mismatch\n got: %+v\nwant: %+v", s, want)
	}
}

func TestDiscoverIncremental(t *testing.T) {
	lines := fixture()
	dir, path := setup(t, lines[:6])
	cacheFile := filepath.Join(t.TempDir(), "index.json")
	cache := index.Open(cacheFile)
	p := New(dir, "claude", nil, cache)
	discoverOne(t, p)

	// Append the rest, with the last line still being written.
	rest := strings.Join(lines[6:], "\n")
	cut := len(rest) - 20
	appendFile(t, path, rest[:cut])
	partial := discoverOne(t, p)
	if partial.HasCost {
		t.Error("a partial last line was parsed")
	}

	appendFile(t, path, rest[cut:]+"\n")
	got := discoverOne(t, p)
	want := discoverOne(t, New(dir, "claude", nil, nil)) // fresh, uncached parse
	if !reflect.DeepEqual(got, want) {
		t.Errorf("incremental parse differs from full parse\n got: %+v\nwant: %+v", got, want)
	}

	// The cache survives a reload from disk.
	if err := cache.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded := discoverOne(t, New(dir, "claude", nil, index.Open(cacheFile)))
	if !reflect.DeepEqual(reloaded, want) {
		t.Errorf("cached summary differs after reload\n got: %+v\nwant: %+v", reloaded, want)
	}
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	// Make sure the mtime differs even on coarse-grained filesystems.
	later := time.Now().Add(time.Second)
	_ = os.Chtimes(path, later, later)
}

func TestLoad(t *testing.T) {
	dir, _ := setup(t, fixture())
	p := New(dir, "claude", nil, nil)
	s := discoverOne(t, p)

	tr, err := p.Load(context.Background(), host.Local{}, s, 20)
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		Role  provider.Role
		Text  string
		Tools []string
		Count int
	}
	var got []entry
	for _, m := range tr.Messages {
		got = append(got, entry{m.Role, m.Text, m.Tools, m.Count})
	}
	want := []entry{
		{provider.RoleUser, "Fix the login bug", nil, 0},
		{provider.RoleTool, "", []string{"Read(/tmp/proj/login.go)", "Bash(go test ./...)"}, 2},
		{provider.RoleAssistant, "Fixed the **bug**.", nil, 0},
		{provider.RoleUser, "/compact keep tests", nil, 0},
		{provider.RoleNotice, "Request interrupted by user", nil, 0},
		{provider.RoleUser, "Now add a test", nil, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries mismatch\n got: %+v\nwant: %+v", got, want)
	}

	tr, err = p.Load(context.Background(), host.Local{}, s, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Messages) != 2 || tr.Messages[1].Text != "Now add a test" {
		t.Errorf("tail of 2 = %+v", tr.Messages)
	}
}

func TestRunning(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	self := os.Getpid()
	start, err := proc.StartTime(self)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, pid int, procStart, id, status string) {
		data := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":"/tmp","status":%q,"procStart":%q,"updatedAt":1793898339518}`, pid, id, status, procStart)
		if err := os.WriteFile(filepath.Join(sessions, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("live.json", self, start, "live", "busy")
	write("reused.json", self, "1", "reused", "idle") // pid reused by another process
	write("dead.json", 1<<22+12345, "", "dead", "idle")

	got, err := New(dir, "claude", nil, nil).Running(context.Background(), host.Local{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["live"].PID != self || got["live"].Status != "busy" {
		t.Errorf("Running = %+v, want only the live session", got)
	}
}

func TestCommands(t *testing.T) {
	p := New(t.TempDir(), "/opt/claude", []string{"--model", "opus"}, nil)
	if got := p.ResumeCmd(provider.Session{ID: "abc"}); !reflect.DeepEqual(got, []string{"/opt/claude", "--resume", "abc", "--model", "opus"}) {
		t.Errorf("ResumeCmd = %q", got)
	}
	if got := p.NewCmd("xyz"); !reflect.DeepEqual(got, []string{"/opt/claude", "--session-id", "xyz", "--model", "opus"}) {
		t.Errorf("NewCmd = %q", got)
	}
}
