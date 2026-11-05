package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
)

// TestClient runs against a real tmux server on a private socket.
func TestClient(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	sock := fmt.Sprintf("asm-test-%d", os.Getpid())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "kill-server").Run() })
	c := New(host.Local{}, sock)
	ctx := context.Background()

	got, err := c.Sessions(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("Sessions with no server = %v, %v; want empty, nil", got, err)
	}

	err = c.NewSession(ctx, "cc-test", t.TempDir(), []string{"sh", "-c", "echo hello-from-pane; sleep 30"})
	if err != nil {
		t.Fatal(err)
	}
	got, err = c.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := got["cc-test"]
	if !ok || len(s.PanePIDs) != 1 || s.Created.IsZero() {
		t.Fatalf("Sessions = %+v", got)
	}

	if p, err := c.Prefix(ctx); err != nil || p == "" {
		t.Errorf("Prefix = %q, %v", p, err)
	}

	var text string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if text, err = c.Capture(ctx, "cc-test"); err == nil && strings.Contains(text, "hello-from-pane") {
			break
		}
	}
	if !strings.Contains(text, "hello-from-pane") {
		t.Errorf("Capture = %q, %v", text, err)
	}

	// Targets match exactly, never by prefix.
	if err := c.KillSession(ctx, "cc-te"); err == nil {
		t.Error("KillSession matched a prefix")
	}
	if err := c.KillSession(ctx, "cc-test"); err != nil {
		t.Fatal(err)
	}
	got, err = c.Sessions(ctx)
	if err != nil || len(got) != 0 {
		t.Errorf("Sessions after kill = %v, %v", got, err)
	}
}
