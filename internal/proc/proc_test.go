package proc

import (
	"os"
	"os/exec"
	"testing"
)

func TestSelf(t *testing.T) {
	pid, ppid := os.Getpid(), os.Getppid()
	start, err := StartTime(pid)
	if err != nil || start == "" {
		t.Fatalf("StartTime = %q, %v", start, err)
	}
	if !Alive(pid, start) || !Alive(pid, "") {
		t.Error("own process is not alive")
	}
	if Alive(pid, start+"0") {
		t.Error("alive with another start time")
	}
	if a := Ancestors(pid); len(a) == 0 || a[0] != ppid {
		t.Errorf("Ancestors(%d) = %v, want parent %d first", pid, a, ppid)
	}
	if !IsDescendant(pid, ppid) || IsDescendant(ppid, pid) {
		t.Error("IsDescendant got the parent relation wrong")
	}
}

func TestExited(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if Alive(pid, "") {
		t.Errorf("exited pid %d is alive", pid)
	}
	if a := Ancestors(pid); len(a) != 0 {
		t.Errorf("Ancestors of exited pid = %v", a)
	}
}
