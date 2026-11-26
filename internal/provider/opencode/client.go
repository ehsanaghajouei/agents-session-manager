// Package opencode connects to the local OpenCode V2 service.
package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
)

// Client uses the OpenCode CLI for local service discovery and authentication.
type Client struct {
	bin string
}

// NewClient configures a client without contacting or starting the service.
// An empty bin uses opencode from PATH.
func NewClient(bin string) *Client {
	if bin == "" {
		bin = "opencode"
	}
	return &Client{bin: bin}
}

// ServerInfo identifies the OpenCode service.
type ServerInfo struct {
	Version string `json:"version"`
	PID     int    `json:"pid"`
}

// Info checks that the local service is reachable and runs V2. The CLI may
// start the shared service if it is not already running.
func (c *Client) Info(ctx context.Context, ex host.Executor) (ServerInfo, error) {
	var info ServerInfo
	if err := c.get(ctx, ex, "/api/info", &info); err != nil {
		return ServerInfo{}, err
	}
	if info.Version == "" {
		return ServerInfo{}, errors.New("opencode API /api/info: missing service version")
	}
	if !strings.HasPrefix(strings.TrimPrefix(info.Version, "v"), "2.") {
		return ServerInfo{}, fmt.Errorf("opencode API /api/info: service version %q is unsupported; OpenCode V2 is required", info.Version)
	}
	return info, nil
}

func (c *Client) get(ctx context.Context, ex host.Executor, path string, dst any) error {
	if !ex.IsLocal() {
		return errors.New("opencode: remote hosts are not supported")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	out, err := ex.Run(ctx, nil, c.bin, "api", "get", path)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("opencode API %s: %w", path, err)
	}
	if err := json.Unmarshal(out, dst); err != nil {
		return fmt.Errorf("opencode API %s: decode response: %w", path, err)
	}
	return nil
}
