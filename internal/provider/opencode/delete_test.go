package opencode

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestDelete(t *testing.T) {
	for _, tt := range []struct {
		name, id, version, output, wantErr string
		runErr                             error
		wantCalls                          int
	}{
		{name: "no content", id: "ses_target", version: "2.4.2", wantCalls: 2},
		{name: "not found", id: "ses_target", version: "2.4.2", runErr: errors.New("HTTP 404 Not Found"), wantErr: "HTTP 404", wantCalls: 2},
		{name: "unexpected output", id: "ses_target", version: "2.4.2", output: `{"error":"unavailable"}`, wantErr: "expected an empty response", wantCalls: 2},
		{name: "invalid ID", id: "ses_target/child", wantErr: "invalid OpenCode session ID"},
		{name: "wrong version", id: "ses_target", version: "1.4.2", wantErr: "OpenCode V2 is required", wantCalls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			ex := executor{local: true, run: func(_ context.Context, _ io.Reader, bin string, args ...string) ([]byte, error) {
				calls++
				switch {
				case calls == 1 && bin == "opencode" && slices.Equal(args, []string{"api", "get", "/api/info"}):
					return []byte(`{"version":"` + tt.version + `"}`), nil
				case calls == 2 && bin == "opencode" && slices.Equal(args, []string{"api", "delete", "/api/session/ses_target"}):
					return []byte(tt.output), tt.runErr
				default:
					t.Fatalf("unexpected API call: %q %q", bin, args)
					return nil, nil
				}
			}}
			err := NewClient("").Delete(t.Context(), ex, tt.id)
			if calls != tt.wantCalls || (err == nil) != (tt.wantErr == "") || err != nil && !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Delete = %v (%d calls); want %q (%d calls)", err, calls, tt.wantErr, tt.wantCalls)
			}
		})
	}
}
