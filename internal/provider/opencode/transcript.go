package opencode

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

type transcriptMessage struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Text        string `json:"text"`
	Description string `json:"description"`
	Outcome     string `json:"outcome"`
	Status      string `json:"status"`
	Time        struct {
		Created int64 `json:"created"`
	} `json:"time"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Name string `json:"name"`
	} `json:"content"`
}

// Load fetches the last n preview entries, returned oldest first for rendering.
// Only the pages needed to fill the preview are requested.
func (c *Client) Load(ctx context.Context, ex host.Executor, s provider.Session, n int) (provider.Transcript, error) {
	if n <= 0 {
		return provider.Transcript{}, nil
	}
	if !strings.HasPrefix(s.ID, "ses") {
		return provider.Transcript{}, errors.New("invalid OpenCode session ID")
	}
	path := "/api/session/" + url.PathEscape(s.ID) + "/message"
	limit := strconv.Itoa(min(max(n, 20), 50))
	query := url.Values{"limit": {limit}, "order": {"desc"}}
	seenCursors := make(map[string]bool)
	seenIDs := make(map[string]bool)
	var tail []provider.Message
	for {
		var page apiPage[transcriptMessage]
		if err := c.get(ctx, ex, path+"?"+query.Encode(), &page); err != nil {
			return provider.Transcript{}, err
		}
		if page.Data == nil || page.Cursor == nil {
			return provider.Transcript{}, fmt.Errorf("opencode API %s: missing page data or cursor", path)
		}
		for _, msg := range page.Data {
			if msg.ID != "" {
				if seenIDs[msg.ID] {
					continue
				}
				seenIDs[msg.ID] = true
			}
			entries := msg.entries()
			slices.Reverse(entries)
			tail = append(tail, entries[:min(len(entries), n-len(tail))]...)
			if len(tail) == n {
				break
			}
		}
		next := page.Cursor.Next
		if len(tail) == n || next == "" {
			break
		}
		if seenCursors[next] {
			return provider.Transcript{}, fmt.Errorf("opencode API %s: repeated pagination cursor", path)
		}
		seenCursors[next] = true
		query = url.Values{"limit": {limit}, "cursor": {next}}
	}
	slices.Reverse(tail)
	return provider.Transcript{Messages: tail}, nil
}

func (msg transcriptMessage) entries() []provider.Message {
	var at time.Time
	if msg.Time.Created != 0 {
		at = time.UnixMilli(msg.Time.Created).UTC()
	}

	var out []provider.Message
	addText := func(role provider.Role, text string) {
		if text = strings.TrimSpace(text); text != "" {
			out = append(out, provider.Message{Role: role, Text: text, Time: at})
		}
	}
	switch msg.Type {
	case "user":
		addText(provider.RoleUser, msg.Text)
	case "assistant":
		const maxToolNames = 3
		for _, part := range msg.Content {
			switch part.Type {
			case "text":
				addText(provider.RoleAssistant, part.Text)
			case "tool":
				name := strings.Join(strings.Fields(part.Name), " ")
				if name == "" {
					name = "Tool"
				}
				if len(out) == 0 || out[len(out)-1].Role != provider.RoleTool {
					out = append(out, provider.Message{Role: provider.RoleTool, Time: at})
				}
				last := &out[len(out)-1]
				last.Count++
				last.Tools = append(last.Tools, name)
				if len(last.Tools) > maxToolNames {
					last.Tools = last.Tools[1:]
				}
			}
		}
	case "synthetic", "system":
		addText(provider.RoleNotice, msg.Description)
	case "compaction":
		switch msg.Status {
		case "completed":
			addText(provider.RoleNotice, "Conversation compacted")
		case "failed":
			addText(provider.RoleNotice, "Compaction failed")
		}
	case "idle":
		if msg.Outcome == "interrupted" {
			addText(provider.RoleNotice, "Request interrupted")
		} else if msg.Outcome == "failed" {
			addText(provider.RoleNotice, "Request failed")
		}
	case "shell":
		out = append(out, provider.Message{Role: provider.RoleTool, Tools: []string{"Shell"}, Count: 1, Time: at})
	}
	return out
}
