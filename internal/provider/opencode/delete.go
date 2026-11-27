package opencode

import (
	"context"
	"errors"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
)

// Delete permanently removes the session and its children from OpenCode.
func (c *Client) Delete(ctx context.Context, ex host.Executor, id string) error {
	if !validSessionID(id) {
		return errors.New("invalid OpenCode session ID")
	}
	if _, err := c.Info(ctx, ex); err != nil {
		return err
	}
	path := "/api/session/" + id
	return c.request(ctx, ex, path, nil, "delete", path)
}
