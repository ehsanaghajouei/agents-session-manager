// Package host runs commands on a machine. Only the local machine exists
// today; an SSH executor will implement the same interface.
package host

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Executor runs commands on one host.
type Executor interface {
	// Name identifies the host in the UI ("local", or an SSH alias).
	Name() string
	// IsLocal reports whether files on the host are readable directly.
	IsLocal() bool
	// Run runs a command to completion and returns its stdout.
	Run(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error)
	// Interactive returns a command meant to take over the user's terminal.
	Interactive(name string, args ...string) *exec.Cmd
}

// Local runs commands on this machine.
type Local struct{}

func (Local) Name() string  { return "local" }
func (Local) IsLocal() bool { return true }

func (Local) Run(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &RunError{Cmd: name, Err: err, Stderr: strings.TrimSpace(stderr.String())}
	}
	return stdout.Bytes(), nil
}

func (Local) Interactive(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// RunError keeps a failed command's stderr for callers that inspect it.
type RunError struct {
	Cmd    string
	Err    error
	Stderr string
}

func (e *RunError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("%s: %v", e.Cmd, e.Err)
	}
	return fmt.Sprintf("%s: %v: %s", e.Cmd, e.Err, e.Stderr)
}

func (e *RunError) Unwrap() error { return e.Err }
