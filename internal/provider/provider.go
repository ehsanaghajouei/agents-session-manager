// Package provider defines what asm needs from an agent (Claude Code today,
// other CLI agents later).
package provider

import (
	"context"
	"time"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
)

// Session is a saved conversation. Fields are plain values so providers can
// cache them as JSON.
type Session struct {
	Provider     string    `json:"provider"`
	Host         string    `json:"host"`
	ID           string    `json:"id"`
	Path         string    `json:"path"` // transcript file
	Title        string    `json:"title,omitempty"`
	Name         string    `json:"name,omitempty"`
	FirstPrompt  string    `json:"firstPrompt,omitempty"`
	LastPrompt   string    `json:"lastPrompt,omitempty"`
	CWD          string    `json:"cwd,omitempty"`
	GitBranch    string    `json:"gitBranch,omitempty"`
	Model        string    `json:"model,omitempty"`
	Version      string    `json:"version,omitempty"`
	Started      time.Time `json:"started"`
	LastActive   time.Time `json:"lastActive"`
	Messages     int       `json:"messages"` // user + assistant records
	Prompts      int       `json:"prompts"`  // prompts typed by the user
	CostUSD      float64   `json:"costUSD"`
	HasCost      bool      `json:"hasCost"`
	Tokens       int64     `json:"tokens"`
	LinesAdded   int       `json:"linesAdded"`
	LinesRemoved int       `json:"linesRemoved"`
	ContinuedIn  string    `json:"continuedIn,omitempty"`
}

// DisplayTitle is the best human label for the session.
func (s Session) DisplayTitle() string {
	switch {
	case s.Title != "":
		return s.Title
	case s.FirstPrompt != "":
		return s.FirstPrompt
	case s.Name != "":
		return s.Name
	}
	return "(untitled)"
}

// Process is a running agent process attached to a session.
type Process struct {
	PID       int
	StartTime string // /proc start ticks, guards against pid reuse
	SessionID string
	Status    string // e.g. "busy", "idle"
	CWD       string
	Name      string
	UpdatedAt time.Time
}

// Role says who produced a transcript entry.
type Role int

const (
	RoleUser Role = iota
	RoleAssistant
	RoleTool   // collapsed tool calls
	RoleNotice // interruptions, compactions, and similar markers
)

// Message is one preview entry.
type Message struct {
	Role  Role
	Text  string
	Tools []string // RoleTool: summaries of the most recent calls
	Count int      // RoleTool: total calls collapsed into this entry
	Time  time.Time
}

// Transcript is the tail of a conversation.
type Transcript struct {
	Messages []Message
}

// Provider discovers and resumes one agent's sessions.
type Provider interface {
	Name() string
	// Discover lists saved sessions on the host.
	Discover(ctx context.Context, ex host.Executor) ([]Session, error)
	// Load returns the last n preview entries of a session.
	Load(ctx context.Context, ex host.Executor, s Session, n int) (Transcript, error)
	// Running lists live processes keyed by session ID.
	Running(ctx context.Context, ex host.Executor) (map[string]Process, error)
	// ResumeCmd is the argv that resumes s.
	ResumeCmd(s Session) []string
	// NewCmd is the argv that starts a new session with the given ID.
	NewCmd(id string) []string
}
