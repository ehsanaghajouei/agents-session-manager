package opencode

import (
	"slices"
	"testing"
)

func TestResumeArgs(t *testing.T) {
	c := NewClient("/example/bin/opencode")
	args, err := c.ResumeArgs("ses_saved")
	if err != nil || !slices.Equal(args, []string{"/example/bin/opencode", "--session", "ses_saved"}) {
		t.Fatalf("ResumeArgs = %q, %v", args, err)
	}
	for _, id := range []string{"", "session/parent", "ses_a:b", "ses_a.b", "ses_?x"} {
		if _, err := c.ResumeArgs(id); err == nil {
			t.Errorf("accepted invalid ID %q", id)
		}
	}
}

func TestSessionDirectory(t *testing.T) {
	for _, tt := range []struct {
		name, response, want string
	}{
		{"found", `{"data":{"id":"ses_saved","location":{"directory":"/work/project"}}}`, "/work/project"},
		{"missing", `{"name":"SessionNotFoundError"}`, ""},
		{"different ID", `{"data":{"id":"ses_other","location":{"directory":"/work/project"}}}`, ""},
		{"relative", `{"data":{"id":"ses_saved","location":{"directory":"project"}}}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ex, check := fixtureExecutor(t, tt.response)
			got, err := NewClient("").SessionDirectory(t.Context(), ex, "ses_saved")
			check("/api/session/ses_saved")
			if got != tt.want || (err != nil) != (tt.want == "") {
				t.Fatalf("SessionDirectory = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
