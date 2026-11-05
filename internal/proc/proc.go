// Package proc reads local process information from /proc (Linux).
package proc

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// stat holds the /proc/<pid>/stat fields asm needs.
type stat struct {
	ppid      int
	startTime string // clock ticks since boot, as written by the kernel
}

func readStat(pid int) (stat, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return stat{}, err
	}
	// The command name (field 2) is in parentheses and may contain spaces,
	// so split after the last ')'. Fields then start at field 3 (state).
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return stat{}, fmt.Errorf("proc %d: malformed stat", pid)
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 20 {
		return stat{}, fmt.Errorf("proc %d: short stat", pid)
	}
	ppid, err := strconv.Atoi(f[1]) // field 4
	if err != nil {
		return stat{}, fmt.Errorf("proc %d: ppid: %w", pid, err)
	}
	return stat{ppid: ppid, startTime: f[19]}, nil // field 22
}

// StartTime returns the process start time as Claude Code records it
// (field 22 of /proc/<pid>/stat).
func StartTime(pid int) (string, error) {
	st, err := readStat(pid)
	return st.startTime, err
}

// Alive reports whether pid exists. When startTime is not empty it must also
// match the process start time, which guards against pid reuse.
func Alive(pid int, startTime string) bool {
	if pid <= 0 {
		return false
	}
	st, err := readStat(pid)
	if err != nil {
		return false
	}
	return startTime == "" || st.startTime == startTime
}

// Terminate sends SIGTERM to pid and waits up to timeout for it to exit.
// startTime guards against signalling a process that reused the pid.
func Terminate(pid int, startTime string, timeout time.Duration) error {
	if !Alive(pid, startTime) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !Alive(pid, startTime) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("pid %d still running %s after SIGTERM", pid, timeout)
}

// Ancestors returns the parent chain of pid, nearest first, ending at pid 1.
func Ancestors(pid int) []int {
	var out []int
	for range 64 { // bounded in case /proc changes under us
		st, err := readStat(pid)
		if err != nil || st.ppid <= 0 {
			break
		}
		out = append(out, st.ppid)
		if st.ppid == 1 {
			break
		}
		pid = st.ppid
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
