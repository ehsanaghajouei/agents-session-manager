package opencode

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

func TestLoadTranscriptPaginatesAndPreservesOrder(t *testing.T) {
	ex, check := fixtureExecutor(t,
		`{"data":[
			{"id":"m3","type":"idle","outcome":"succeeded"},
			{"id":"m2","type":"assistant","time":{"created":1795712842670},"content":[
				{"type":"text","text":"First assistant text"},
				{"type":"tool","name":"read"},
				{"type":"tool","name":"edit"},
				{"type":"reasoning","text":"private reasoning"},
				{"type":"tool","name":"grep"},
				{"type":"tool","name":"shell"},
				{"type":"text","text":"Last assistant text"}]}],
			"cursor":{"next":"a+b/="}}`,
		`{"data":[
			{"id":"m2","type":"assistant","content":[{"type":"text","text":"duplicate"}]},
			{"id":"m1","type":"user","text":"Most recent prompt"},
			{"id":"m0","type":"user","text":"Too old"}],
			"cursor":{"next":"unused"}}`,
	)
	got, err := NewClient("").Load(t.Context(), ex, provider.Session{ID: "ses_fixture"}, 4)
	check("/api/session/ses_fixture/message?limit=20&order=desc", "/api/session/ses_fixture/message?cursor=a%2Bb%2F%3D&limit=20")
	at := time.UnixMilli(1795712842670).UTC()
	want := []provider.Message{
		{Role: provider.RoleUser, Text: "Most recent prompt"},
		{Role: provider.RoleAssistant, Text: "First assistant text", Time: at},
		{Role: provider.RoleTool, Tools: []string{"edit", "grep", "shell"}, Count: 4, Time: at},
		{Role: provider.RoleAssistant, Text: "Last assistant text", Time: at},
	}
	if err != nil || !reflect.DeepEqual(got.Messages, want) {
		t.Fatalf("Load = %+v, %v; want %+v", got.Messages, err, want)
	}
}

func TestLoadTranscriptEmptyAndInvalidResponses(t *testing.T) {
	for _, tt := range []struct {
		name, first, second, want string
	}{
		{name: "empty", first: `{"data":[],"cursor":{"next":null}}`},
		{name: "malformed", first: `{"error":"not found"}`, want: "missing page data or cursor"},
		{
			name: "repeated cursor", first: `{"data":[{"type":"unknown"}],"cursor":{"next":"again"}}`,
			second: `{"data":[{"type":"user","text":"Partial"}],"cursor":{"next":"again"}}`,
			want:   "repeated pagination cursor",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const path = "/api/session/ses_fixture/message?"
			replies := []string{tt.first}
			paths := []string{path + "limit=20&order=desc"}
			if tt.second != "" {
				replies = append(replies, tt.second)
				paths = append(paths, path+"cursor=again&limit=20")
			}
			ex, check := fixtureExecutor(t, replies...)
			got, err := NewClient("").Load(t.Context(), ex, provider.Session{ID: "ses_fixture"}, 2)
			check(paths...)
			if tt.want == "" {
				if err != nil || len(got.Messages) != 0 {
					t.Fatalf("Load = %+v, %v; want empty transcript", got, err)
				}
			} else if len(got.Messages) != 0 || err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load = %+v, %v; want %q and no transcript", got, err, tt.want)
			}
		})
	}
}
