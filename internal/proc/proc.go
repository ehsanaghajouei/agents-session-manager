// Package proc reads local process information: /proc on Linux, sysctl and
// ps on macOS.
package proc

import (
	"fmt"
	"syscall"
	"time"
)

// StartTime returns the process start time as Claude Code records it in
// procStart: field 22 of /proc/<pid>/stat on Linux, the ps lstart column on
// macOS.
func StartTime(pid int) (string, error) {
	return startTime(pid)
}

// Alive reports whether pid exists. When start is not empty it must also
// match the process start time, which guards against pid reuse.
func Alive(pid int, start string) bool {
	if pid <= 0 {
		return false
	}
	st, err := startTime(pid)
	if err != nil {
		return false
	}
	return start == "" || st == start
}

// Terminate sends SIGTERM to pid and waits up to timeout for it to exit.
// start guards against signalling a process that reused the pid.
func Terminate(pid int, start string, timeout time.Duration) error {
	if !Alive(pid, start) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !Alive(pid, start) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("pid %d still running %s after SIGTERM", pid, timeout)
}

// Ancestors returns the parent chain of pid, nearest first, ending at pid 1.
func Ancestors(pid int) []int {
	var out []int
	for range 64 { // bounded in case the process table changes under us
		ppid, err := parent(pid)
		if err != nil || ppid <= 0 {
			break
		}
		out = append(out, ppid)
		if ppid == 1 {
			break
		}
		pid = ppid
	}
	return out
}

// IsDescendant reports whether pid is ancestor itself or runs below it.
func IsDescendant(pid, ancestor int) bool {
	if pid == ancestor {
		return true
	}
	for _, p := range Ancestors(pid) {
		if p == ancestor {
			return true
		}
	}
	return false
}
