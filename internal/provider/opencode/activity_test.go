package opencode

import (
	"maps"
	"testing"
)

func TestActive(t *testing.T) {
	for _, tt := range []struct {
		name, response string
		want           map[string]bool
	}{
		{"running", `{"data":{"ses_active":{"type":"running"},"ses_other":{"type":"future"}}}`, map[string]bool{"ses_active": true}},
		{"empty", `{"data":{}}`, map[string]bool{}},
		{"missing", `{"error":"not available"}`, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ex, check := fixtureExecutor(t, tt.response)
			got, err := NewClient("").Active(t.Context(), ex)
			check("/api/session/active")
			if !maps.Equal(got, tt.want) || (err != nil) != (tt.want == nil) {
				t.Fatalf("Active = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}
