package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

// maxToolLines is how many calls a collapsed tool entry keeps.
const maxToolLines = 3

func wantMessage(t string) bool { return t == "user" || t == "assistant" }

// loadTail reads the whole transcript and keeps the last n preview entries.
// Consecutive tool calls collapse into one entry.
func loadTail(ctx context.Context, path string, n int) (provider.Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return provider.Transcript{}, err
	}
	defer f.Close()

	var tail []provider.Message
	add := func(m provider.Message) {
		if m.Role == provider.RoleTool && len(tail) > 0 && tail[len(tail)-1].Role == provider.RoleTool {
			last := &tail[len(tail)-1]
			last.Count++
			last.Tools = append(last.Tools, m.Tools...)
			if len(last.Tools) > maxToolLines {
				last.Tools = last.Tools[len(last.Tools)-maxToolLines:]
			}
			last.Time = m.Time
			return
		}
		tail = append(tail, m)
		if len(tail) > 2*n { // trim in batches, not on every append
			tail = append(tail[:0], tail[len(tail)-n:]...)
		}
	}

	r := bufio.NewReaderSize(f, 256<<10)
	for lines := 0; ; lines++ {
		if lines%1000 == 0 && ctx.Err() != nil {
			return provider.Transcript{}, ctx.Err()
		}
		line, err := r.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return provider.Transcript{}, err
		}
		if line = bytes.TrimRight(line, "\r\n"); len(line) > 0 && !skippable(line, wantMessage) {
			var rec record
			if json.Unmarshal(line, &rec) == nil {
				for _, m := range previewEntries(&rec) {
					add(m)
				}
			}
		}
		if err != nil { // io.EOF
			break
		}
	}
	if len(tail) > n {
		tail = tail[len(tail)-n:]
	}
	return provider.Transcript{Messages: tail}, nil
}

// previewEntries converts a record into the entries the preview shows.
func previewEntries(r *record) []provider.Message {
	if r.IsSidechain {
		return nil
	}
	t := r.time()
	switch r.Type {
	case "user":
		kind, text := classifyUser(r)
		switch kind {
		case userPrompt, userCommand, userBash:
			return []provider.Message{{Role: provider.RoleUser, Text: text, Time: t}}
		case userNotice:
			return []provider.Message{{Role: provider.RoleNotice, Text: text, Time: t}}
		}
	case "assistant":
		var out []provider.Message
		for _, b := range r.Message.blocks() {
			switch b.Type {
			case "text":
				if s := strings.TrimSpace(b.Text); s != "" && s != "(no content)" {
					out = append(out, provider.Message{Role: provider.RoleAssistant, Text: s, Time: t})
				}
			case "tool_use":
				out = append(out, provider.Message{Role: provider.RoleTool, Tools: []string{toolSummary(b)}, Count: 1, Time: t})
			}
		}
		return out
	}
	return nil
}
