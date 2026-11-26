package opencode

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"testing"
)

func TestCreate(t *testing.T) {
	for _, tt := range []struct {
		name, version, response, want string
		wantCalls                     int
	}{
		{"created", "2.4.2", `{"data":{"id":"ses_created"}}`, "ses_created", 2},
		{"bad ID", "2.4.2", `{"data":{"id":"ses_bad:id"}}`, "", 2},
		{"API error envelope", "2.4.2", `{"name":"InvalidRequestError"}`, "", 2},
		{"wrong version", "1.4.2", "", "", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const dir = `/tmp/project with "quotes"`
			calls := 0
			ex := executor{local: true, run: func(_ context.Context, _ io.Reader, bin string, args ...string) ([]byte, error) {
				calls++
				switch calls {
				case 1:
					if bin != "opencode" || !slices.Equal(args, []string{"api", "get", "/api/info"}) {
						t.Fatalf("unexpected API call: %q %q", bin, args)
					}
					return []byte(`{"version":"` + tt.version + `"}`), nil
				case 2:
					if len(args) != 5 || !slices.Equal(args[:4], []string{"api", "post", "/api/session", "--data"}) {
						t.Fatalf("unexpected create call: %q", args)
					}
					var body struct {
						Location map[string]string `json:"location"`
					}
					if err := json.Unmarshal([]byte(args[4]), &body); err != nil || body.Location["directory"] != dir {
						t.Fatalf("invalid create body: %q, %v", args[4], err)
					}
					return []byte(tt.response), nil
				default:
					t.Fatalf("unexpected API call: %q", args)
					return nil, nil
				}
			}}
			got, err := NewClient("").Create(t.Context(), ex, dir)
			if got != tt.want || (err == nil) != (tt.want != "") || calls != tt.wantCalls {
				t.Fatalf("Create = %q, %v (%d calls); want %q (%d calls)", got, err, calls, tt.want, tt.wantCalls)
			}
		})
	}
}
