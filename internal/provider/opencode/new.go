package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
)

// Create opens a saved session at dir without sending a prompt.
func (c *Client) Create(ctx context.Context, ex host.Executor, dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("the OpenCode session's working directory must be absolute")
	}
	if _, err := c.Info(ctx, ex); err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Location map[string]string `json:"location"`
	}{Location: map[string]string{"directory": dir}})
	if err != nil {
		return "", err
	}
	const path = "/api/session"
	var response struct {
		Data sessionInfo `json:"data"`
	}
	if err := c.request(ctx, ex, path, &response, "post", path, "--data", string(data)); err != nil {
		return "", err
	}
	if !validSessionID(response.Data.ID) {
		return "", errors.New("OpenCode did not return a valid new session ID; refresh the list before retrying")
	}
	return response.Data.ID, nil
}
