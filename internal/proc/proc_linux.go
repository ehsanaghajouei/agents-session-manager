package proc

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
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

func startTime(pid int) (string, error) {
	st, err := readStat(pid)
	return st.startTime, err
}

func parent(pid int) (int, error) {
	st, err := readStat(pid)
	return st.ppid, err
}
