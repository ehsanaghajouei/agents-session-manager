package proc

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestStartTimeMatchesClaude compares StartTime with the command Claude Code
// runs on macOS to fill procStart.
func TestStartTimeMatchesClaude(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	out, err := exec.Command("sh", "-c", "LC_ALL=C TZ=UTC ps -o lstart= -p "+pid).Output()
	if err != nil {
		t.Fatal(err)
	}
	got, err := StartTime(os.Getpid())
	if want := strings.TrimSpace(string(out)); err != nil || got != want {
		t.Errorf("StartTime = %q, %v; want %q", got, err, want)
	}
}
