package proc

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// startTime runs ps the way Claude Code does on macOS, so the result matches
// the procStart it records.
func startTime(pid int) (string, error) {
	cmd := exec.Command("/bin/ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	out, err := cmd.Output() // ps exits non-zero when pid does not exist
	if err != nil {
		return "", fmt.Errorf("proc %d: ps: %w", pid, err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", fmt.Errorf("proc %d: no start time", pid)
	}
	return s, nil
}

func parent(pid int) (int, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, fmt.Errorf("proc %d: %w", pid, err)
	}
	return int(kp.Eproc.Ppid), nil
}
