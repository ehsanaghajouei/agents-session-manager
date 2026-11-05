package claude

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// record is one line of a transcript. Only the fields asm reads are listed;
// the format is internal to Claude Code, so unknown fields are ignored.
type record struct {
	Type             string   `json:"type"`
	Timestamp        string   `json:"timestamp"`
	CWD              string   `json:"cwd"`
	GitBranch        string   `json:"gitBranch"`
	Version          string   `json:"version"`
	IsSidechain      bool     `json:"isSidechain"`
	IsMeta           bool     `json:"isMeta"`
	IsCompactSummary bool     `json:"isCompactSummary"`
	Message          *message `json:"message"`

	AITitle     string `json:"aiTitle"`              // ai-title
	AgentName   string `json:"agentName"`            // agent-name
	LastPrompt  string `json:"lastPrompt"`           // last-prompt
	ContinuedIn string `json:"continuedInSessionId"` // continued-in

	// cost-state
	TotalCostUSD      *float64              `json:"totalCostUSD"`
	TotalLinesAdded   int                   `json:"totalLinesAdded"`
	TotalLinesRemoved int                   `json:"totalLinesRemoved"`
	ModelUsage        map[string]modelUsage `json:"modelUsage"`
}

type modelUsage struct {
	InputTokens              int64 `json:"inputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
}

type message struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"` // a string or a list of blocks
}

type block struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`  // tool_use
	Input json.RawMessage `json:"input"` // tool_use
}

func (m *message) blocks() []block {
	if m == nil || len(m.Content) == 0 {
		return nil
	}
	if m.Content[0] == '"' {
		var s string
		if json.Unmarshal(m.Content, &s) != nil {
			return nil
		}
		return []block{{Type: "text", Text: s}}
	}
	var bs []block
	_ = json.Unmarshal(m.Content, &bs)
	return bs
}

func (r *record) time() time.Time {
	t, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
	return t
}

// metaTypes are the non-message records asm reads.
var metaTypes = map[string]bool{
	"ai-title":     true,
	"agent-name":   true,
	"last-prompt":  true,
	"cost-state":   true,
	"continued-in": true,
}

var typePrefix = []byte(`{"type":"`)

// skippable reports whether a line can be ignored without decoding it.
// Most bookkeeping records (some several MB) start with their type, so a
// prefix check avoids parsing them. Lines in other shapes are decoded.
func skippable(line []byte, wanted func(string) bool) bool {
	if !bytes.HasPrefix(line, typePrefix) {
		return false
	}
	rest := line[len(typePrefix):]
	i := bytes.IndexByte(rest, '"')
	return i >= 0 && !wanted(string(rest[:i]))
}

type userKind int

const (
	userHidden  userKind = iota // tool results, notifications, command output
	userPrompt                  // text the user typed
	userCommand                 // slash command
	userBash                    // ! shell input
	userNotice                  // interruption or compaction marker
)

// hiddenTags wrap text that Claude Code injects as user messages.
var hiddenTags = map[string]bool{
	"task-notification":       true,
	"local-command-stdout":    true,
	"local-command-stderr":    true,
	"local-command-caveat":    true,
	"bash-stdout":             true,
	"bash-stderr":             true,
	"system-reminder":         true,
	"user-prompt-submit-hook": true,
}

var leadingTag = regexp.MustCompile(`^<([a-z-]+)>`)

// classifyUser decides how a user record appears in the preview.
func classifyUser(r *record) (userKind, string) {
	if r.IsMeta || r.IsSidechain || r.Message == nil {
		return userHidden, ""
	}
	if r.IsCompactSummary {
		return userNotice, "conversation compacted"
	}
	var text string
	image := false
	for _, b := range r.Message.blocks() {
		switch b.Type {
		case "tool_result":
			return userHidden, ""
		case "text":
			if text == "" {
				text = b.Text
			}
		case "image":
			image = true
		}
	}
	t := strings.TrimSpace(text)
	switch {
	case t == "" && image:
		return userPrompt, "[image]"
	case t == "":
		return userHidden, ""
	case strings.HasPrefix(t, "[Request interrupted"):
		return userNotice, strings.Trim(t, "[]")
	case strings.HasPrefix(t, "<command-name>"), strings.HasPrefix(t, "<command-message>"):
		cmd := strings.TrimSpace(tagValue(t, "command-name") + " " + tagValue(t, "command-args"))
		return userCommand, cmd
	case strings.HasPrefix(t, "<bash-input>"):
		return userBash, "! " + tagValue(t, "bash-input")
	}
	if m := leadingTag.FindStringSubmatch(t); m != nil && hiddenTags[m[1]] {
		return userHidden, ""
	}
	return userPrompt, t
}

func tagValue(s, tag string) string {
	open, close := "<"+tag+">", "</"+tag+">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	s = s[i+len(open):]
	if j := strings.Index(s, close); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// toolKeys are tool inputs that best describe a call, most telling first.
var toolKeys = []string{"command", "file_path", "pattern", "path", "url", "query", "skill", "description", "prompt"}

func toolSummary(b block) string {
	var in map[string]any
	_ = json.Unmarshal(b.Input, &in)
	for _, k := range toolKeys {
		if v, ok := in[k].(string); ok && v != "" {
			return b.Name + "(" + oneLine(v, 80) + ")"
		}
	}
	return b.Name
}

// oneLine returns the first line of s, cut to max runes.
func oneLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}
