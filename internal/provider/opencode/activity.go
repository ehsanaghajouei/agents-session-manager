package opencode

import (
	"context"
	"errors"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
)

// Active returns sessions whose agent is working in the shared service.
// Idle clients are not visible to this API.
func (c *Client) Active(ctx context.Context, ex host.Executor) (map[string]bool, error) {
	const path = "/api/session/active"
	var response struct {
		Data map[string]struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := c.get(ctx, ex, path, &response); err != nil {
		return nil, err
	}
	if response.Data == nil {
		return nil, errors.New("opencode API /api/session/active: missing activity data")
	}
	active := make(map[string]bool, len(response.Data))
	for id, status := range response.Data {
		if status.Type == "running" {
			active[id] = true
		}
	}
	return active, nil
}
