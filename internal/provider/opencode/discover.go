package opencode

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

type sessionPage struct {
	Data   []sessionInfo `json:"data"`
	Cursor *struct {
		Next string `json:"next"`
	} `json:"cursor"`
}

type sessionInfo struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Cost     *float64 `json:"cost"`
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
	Model struct {
		ID string `json:"id"`
	} `json:"model"`
	Time struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
	Tokens struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

// Discover lists saved sessions newest first. If a later page fails, it
// returns the sessions already read along with the error.
func (c *Client) Discover(ctx context.Context, ex host.Executor) ([]provider.Session, error) {
	if _, err := c.Info(ctx, ex); err != nil {
		return nil, err
	}
	const path = "/api/session"
	hostName := ex.Name()
	var sessions []provider.Session
	seenIDs := make(map[string]bool)
	seenCursors := make(map[string]bool)
	query := url.Values{"order": {"desc"}}
	for {
		var page sessionPage
		if err := c.get(ctx, ex, path+"?"+query.Encode(), &page); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return sessions, err
		}
		if page.Data == nil || page.Cursor == nil {
			return sessions, errors.New("opencode API /api/session: missing page data or cursor")
		}
		for _, s := range page.Data {
			if s.ID == "" {
				return sessions, errors.New("opencode API /api/session: missing session ID")
			}
			if !seenIDs[s.ID] {
				sessions = append(sessions, s.session(hostName))
				seenIDs[s.ID] = true
			}
		}
		next := page.Cursor.Next
		if next == "" {
			if sessions == nil {
				sessions = []provider.Session{}
			}
			return sessions, nil
		}
		if seenCursors[next] {
			return sessions, errors.New("opencode API /api/session: repeated pagination cursor")
		}
		seenCursors[next] = true
		query = url.Values{"cursor": {next}}
	}
}

func (s sessionInfo) session(hostName string) provider.Session {
	result := provider.Session{
		Provider:            "opencode",
		Host:                hostName,
		ID:                  s.ID,
		Title:               s.Title,
		CWD:                 s.Location.Directory,
		Model:               s.Model.ID,
		MessageCountUnknown: true,
		Tokens:              s.Tokens.Input + s.Tokens.Output + s.Tokens.Reasoning + s.Tokens.Cache.Read + s.Tokens.Cache.Write,
	}
	if s.Time.Created != 0 {
		result.Started = time.UnixMilli(s.Time.Created).UTC()
	}
	if s.Time.Updated != 0 {
		result.LastActive = time.UnixMilli(s.Time.Updated).UTC()
	}
	if s.Cost != nil {
		result.CostUSD = *s.Cost
		result.HasCost = true
	}
	return result
}
