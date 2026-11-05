// Package tmux wraps the tmux commands asm uses. Commands go through a
// host.Executor so the same code can later drive tmux on remote hosts.
package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
)

// Session is a tmux session and the pids of its panes.
type Session struct {
	Name     string
	Attached bool
	Created  time.Time
	PanePIDs []int
}

// Client runs tmux through an executor.
type Client struct {
	ex     host.Executor
	socket string // -L socket name; empty means tmux's default server
}

// New returns a client for the default tmux server. socket selects another
// server (tmux -L), which tests use to stay isolated.
func New(ex host.Executor, socket string) *Client {
	return &Client{ex: ex, socket: socket}
}

func (c *Client) args(a ...string) []string {
	if c.socket == "" {
		return a
	}
	return append([]string{"-L", c.socket}, a...)
}

func (c *Client) run(ctx context.Context, a ...string) ([]byte, error) {
	return c.ex.Run(ctx, nil, "tmux", c.args(a...)...)
}

// target matches a session name exactly instead of by prefix.
func target(name string) string { return "=" + name }

// Sessions lists sessions by name. No running server means no sessions.
func (c *Client) Sessions(ctx context.Context) (map[string]Session, error) {
	out, err := c.run(ctx, "list-sessions", "-F", "#{session_name}\t#{session_attached}\t#{session_created}")
	if err != nil {
		if noServer(err) {
			return map[string]Session{}, nil
		}
		return nil, err
	}
	sessions := map[string]Session{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		attached, _ := strconv.Atoi(f[1])
		created, _ := strconv.ParseInt(f[2], 10, 64)
		sessions[f[0]] = Session{Name: f[0], Attached: attached > 0, Created: time.Unix(created, 0)}
	}

	out, err = c.run(ctx, "list-panes", "-a", "-F", "#{session_name}\t#{pane_pid}")
	if err != nil {
		if noServer(err) { // server exited between the two calls
			return map[string]Session{}, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, pidStr, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		s, found := sessions[name]
		pid, err := strconv.Atoi(pidStr)
		if !found || err != nil {
			continue
		}
		s.PanePIDs = append(s.PanePIDs, pid)
		sessions[name] = s
	}
	return sessions, nil
}

// NewSession starts a detached session running argv in dir. argv runs
// directly, without a shell, so its arguments need no quoting.
func (c *Client) NewSession(ctx context.Context, name, dir string, argv []string) error {
	a := []string{"new-session", "-d", "-s", name, "-c", dir}
	_, err := c.run(ctx, append(a, argv...)...)
	return err
}

// KillSession ends a session and the processes in it.
func (c *Client) KillSession(ctx context.Context, name string) error {
	_, err := c.run(ctx, "kill-session", "-t", target(name))
	return err
}

// Capture returns the visible text of the session's active pane.
func (c *Client) Capture(ctx context.Context, name string) (string, error) {
	out, err := c.run(ctx, "capture-pane", "-p", "-J", "-t", target(name)+":")
	return string(out), err
}

// Prefix returns the server's prefix key in tmux notation, e.g. "C-b".
func (c *Client) Prefix(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "show-options", "-gv", "prefix")
	return strings.TrimSpace(string(out)), err
}

// AttachCmd returns the command that shows the session in this terminal.
// Inside tmux, attaching would nest servers, so the client switches instead.
func (c *Client) AttachCmd(name string) *exec.Cmd {
	if os.Getenv("TMUX") != "" {
		return c.ex.Interactive("tmux", c.args("switch-client", "-t", target(name))...)
	}
	return c.ex.Interactive("tmux", c.args("attach-session", "-t", target(name))...)
}

func noServer(err error) bool {
	var re *host.RunError
	if !errors.As(err, &re) {
		return false
	}
	return strings.Contains(re.Stderr, "no server running") ||
		strings.Contains(re.Stderr, "error connecting to")
}
