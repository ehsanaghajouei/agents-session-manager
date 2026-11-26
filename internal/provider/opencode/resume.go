package opencode

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
)

// ResumeArgs returns the argv for opening a specific session in OpenCode V2.
func (c *Client) ResumeArgs(id string) ([]string, error) {
	if !validSessionID(id) {
		return nil, errors.New("invalid OpenCode session ID")
	}
	bin := c.bin
	// tmux may have a different PATH from asm.
	if path, err := exec.LookPath(bin); err == nil {
		bin = path
	}
	return []string{bin, "--session", id}, nil
}

// SessionDirectory verifies the session still exists and returns its current
// directory. The CLI would otherwise create a session for a missing ID.
func (c *Client) SessionDirectory(ctx context.Context, ex host.Executor, id string) (string, error) {
	if !validSessionID(id) {
		return "", errors.New("invalid OpenCode session ID")
	}
	var response struct {
		Data sessionInfo `json:"data"`
	}
	path := "/api/session/" + id
	if err := c.get(ctx, ex, path, &response); err != nil {
		return "", err
	}
	info := response.Data
	if info.ID != id {
		return "", fmt.Errorf("OpenCode session %q could not be verified", id)
	}
	if !filepath.IsAbs(info.Location.Directory) {
		return "", errors.New("the OpenCode session's working directory is unknown or not absolute")
	}
	return info.Location.Directory, nil
}

// The ID must be safe as both a path segment and part of a tmux name.
func validSessionID(id string) bool {
	return strings.HasPrefix(id, "ses") && len(id) > 3 && strings.IndexFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) < 0
}
