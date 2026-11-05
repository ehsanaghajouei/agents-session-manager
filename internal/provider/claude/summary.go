package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

func wantSummary(t string) bool { return metaTypes[t] || t == "user" || t == "assistant" }

// parseFrom reads complete lines of path starting at offset and folds them
// into s. It returns the offset after the last complete line; a partial last
// line (still being written) is left for the next call.
func parseFrom(path string, offset int64, s *provider.Session) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}

	r := bufio.NewReaderSize(f, 256<<10)
	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return offset, nil
		}
		if err != nil {
			return offset, err
		}
		offset += int64(len(line))

		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 || skippable(line, wantSummary) {
			continue
		}
		var rec record
		if json.Unmarshal(line, &rec) != nil {
			continue // tolerate lines in formats we do not know
		}
		apply(s, &rec)
	}
}

// apply folds one record into the session summary.
func apply(s *provider.Session, r *record) {
	switch r.Type {
	case "user", "assistant":
		if r.IsSidechain {
			return
		}
		s.Messages++
		if t := r.time(); !t.IsZero() {
			if s.Started.IsZero() || t.Before(s.Started) {
				s.Started = t
			}
			if t.After(s.LastActive) {
				s.LastActive = t
			}
		}
		if r.CWD != "" {
			s.CWD = r.CWD
		}
		if r.GitBranch != "" {
			s.GitBranch = r.GitBranch
		}
		if r.Version != "" {
			s.Version = r.Version
		}
		if r.Type == "assistant" {
			if m := r.Message; m != nil && m.Model != "" && m.Model != "<synthetic>" {
				s.Model = m.Model
			}
			return
		}
		if kind, text := classifyUser(r); kind == userPrompt {
			s.Prompts++
			if s.FirstPrompt == "" {
				s.FirstPrompt = oneLine(text, 200)
			}
		}
	case "ai-title":
		if r.AITitle != "" {
			s.Title = r.AITitle
		}
	case "agent-name":
		if r.AgentName != "" {
			s.Name = r.AgentName
		}
	case "last-prompt":
		if r.LastPrompt != "" {
			s.LastPrompt = oneLine(r.LastPrompt, 200)
		}
	case "continued-in":
		s.ContinuedIn = r.ContinuedIn
	case "cost-state":
		if r.TotalCostUSD == nil {
			return
		}
		s.HasCost = true
		s.CostUSD = *r.TotalCostUSD
		s.LinesAdded = r.TotalLinesAdded
		s.LinesRemoved = r.TotalLinesRemoved
		s.Tokens = 0
		for _, u := range r.ModelUsage {
			s.Tokens += u.InputTokens + u.OutputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
		}
	}
}
