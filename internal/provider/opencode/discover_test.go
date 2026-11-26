package opencode

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

func TestDiscover(t *testing.T) {
	ex, check := discoveryExecutor(t, `{"version":"2.4.2"}`,
		`{"data":[{"id":"ses_one","title":"Fix tests","cost":0.25,"location":{"directory":"/work/project"},
			"model":{"id":"model-fixture","providerID":"provider-fixture"},
			"time":{"created":1795712842501,"updated":1795712842670},
			"tokens":{"input":10,"output":20,"reasoning":30,"cache":{"read":40,"write":50}}}],"cursor":{"next":"a+/=&?"}}`,
		`{"data":[{"id":"ses_one"},{"id":"ses_two","cost":0}],"cursor":{"previous":"ignore","next":"last"}}`,
		`{"data":[],"cursor":{"next":null}}`,
	)
	got, err := NewClient("").Discover(t.Context(), ex)
	check("/api/info", "/api/session?order=desc", "/api/session?cursor=a%2B%2F%3D%26%3F", "/api/session?cursor=last")
	want := []provider.Session{
		{
			Provider: "opencode", Host: "fixture-host", ID: "ses_one",
			Title: "Fix tests", CWD: "/work/project", Model: "model-fixture",
			Started: time.UnixMilli(1795712842501).UTC(), LastActive: time.UnixMilli(1795712842670).UTC(),
			CostUSD: 0.25, HasCost: true, Tokens: 150, MessageCountUnknown: true,
		},
		{Provider: "opencode", Host: "fixture-host", ID: "ses_two", HasCost: true, MessageCountUnknown: true},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("Discover = %+v, %v; want %+v", got, err, want)
	}
}

func TestDiscoverEmpty(t *testing.T) {
	ex, check := discoveryExecutor(t, `{"version":"2.4.2"}`, `{"data":[],"cursor":{}}`)
	got, err := NewClient("").Discover(t.Context(), ex)
	check("/api/info", "/api/session?order=desc")
	if got == nil || len(got) != 0 || err != nil {
		t.Fatalf("Discover = %+v, %v; want an empty list", got, err)
	}
}

func TestDiscoverErrors(t *testing.T) {
	for _, tt := range []struct {
		name, info, first, next, want string
		wantRows                      int
	}{
		{name: "unsupported server", info: `{"version":"1.4.2"}`, want: "OpenCode V2 is required"},
		{name: "invalid envelope", first: `{"error":"unavailable"}`, want: "missing page data or cursor"},
		{name: "missing ID", first: `{"data":[{}],"cursor":{}}`, want: "missing session ID"},
		{
			name: "bad second page", first: `{"data":[{"id":"ses_one"}],"cursor":{"next":"cursor"}}`,
			next: "bad JSON", want: "decode response", wantRows: 1,
		},
		{
			name: "repeated cursor", first: `{"data":[{"id":"ses_one"}],"cursor":{"next":"cursor"}}`,
			next: `{"data":[{"id":"ses_two"}],"cursor":{"next":"cursor"}}`, want: "repeated pagination cursor", wantRows: 2,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := tt.info
			if info == "" {
				info = `{"version":"2.4.2"}`
			}
			replies := []string{info}
			paths := []string{"/api/info"}
			if tt.first != "" {
				replies = append(replies, tt.first)
				paths = append(paths, "/api/session?order=desc")
			}
			if tt.next != "" {
				replies = append(replies, tt.next)
				paths = append(paths, "/api/session?cursor=cursor")
			}
			ex, check := discoveryExecutor(t, replies...)
			got, err := NewClient("").Discover(t.Context(), ex)
			check(paths...)
			if err == nil || !strings.Contains(err.Error(), tt.want) || len(got) != tt.wantRows {
				t.Fatalf("Discover = %+v, %v; want %d rows and %q", got, err, tt.wantRows, tt.want)
			}
			if tt.wantRows == 0 && got != nil {
				t.Fatal("a failed first page must not look like a successful empty list")
			}
		})
	}
}

func discoveryExecutor(t *testing.T, replies ...string) (executor, func(...string)) {
	t.Helper()
	var paths []string
	ex := executor{local: true, run: func(_ context.Context, stdin io.Reader, bin string, args ...string) ([]byte, error) {
		if stdin != nil || bin != "opencode" || len(args) != 3 || args[0] != "api" || args[1] != "get" || len(paths) >= len(replies) {
			t.Fatalf("unexpected API call: %q %q", bin, args)
		}
		reply := replies[len(paths)]
		paths = append(paths, args[2])
		return []byte(reply), nil
	}}
	return ex, func(want ...string) {
		t.Helper()
		if !slices.Equal(paths, want) {
			t.Fatalf("API paths = %q; want %q", paths, want)
		}
	}
}
