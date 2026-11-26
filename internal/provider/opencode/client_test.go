package opencode

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
)

type executor struct {
	host.Executor
	local bool
	run   func(context.Context, io.Reader, string, ...string) ([]byte, error)
}

func (e executor) Name() string  { return "fixture-host" }
func (e executor) IsLocal() bool { return e.local }

func (e executor) Run(ctx context.Context, stdin io.Reader, bin string, args ...string) ([]byte, error) {
	return e.run(ctx, stdin, bin, args...)
}

func TestInfo(t *testing.T) {
	for _, tt := range []struct {
		bin, wantBin, version string
	}{
		{"", "opencode", "2.4.2"},
		{"/custom/opencode", "/custom/opencode", "v2.4.2"},
	} {
		t.Run(tt.wantBin, func(t *testing.T) {
			ctx := t.Context()
			ex := executor{local: true, run: func(gotCtx context.Context, stdin io.Reader, bin string, args ...string) ([]byte, error) {
				if gotCtx != ctx || stdin != nil || bin != tt.wantBin || !slices.Equal(args, []string{"api", "get", "/api/info"}) {
					t.Fatalf("unexpected API call: %q %q", bin, args)
				}
				return []byte(`{"version":"` + tt.version + `","pid":123,"urls":[],"paths":{"tmp":"/tmp"}}`), nil
			}}
			info, err := NewClient(tt.bin).Info(ctx, ex)
			if err != nil || info.Version != tt.version || info.PID != 123 {
				t.Fatalf("Info = %+v, %v", info, err)
			}
		})
	}
}

func TestInfoErrors(t *testing.T) {
	for _, tt := range []struct {
		name, out, want string
		err             error
	}{
		{name: "missing CLI", err: fs.ErrNotExist, want: "/api/info"},
		{name: "invalid JSON", out: "not JSON", want: "decode response"},
		{name: "missing version", out: `{"pid":123}`, want: "missing service version"},
		{name: "V1", out: `{"version":"1.4.2"}`, want: "OpenCode V2 is required"},
		{name: "future major", out: `{"version":"20.0.0"}`, want: "OpenCode V2 is required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ex := executor{local: true, run: func(context.Context, io.Reader, string, ...string) ([]byte, error) {
				return []byte(tt.out), tt.err
			}}
			_, err := NewClient("").Info(t.Context(), ex)
			if err == nil || !strings.Contains(err.Error(), tt.want) || (tt.err != nil && !errors.Is(err, tt.err)) {
				t.Fatalf("Info error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestInfoRejectsRemoteExecutor(t *testing.T) {
	_, err := NewClient("").Info(t.Context(), executor{})
	if err == nil || !strings.Contains(err.Error(), "remote hosts are not supported") {
		t.Fatalf("Info error = %v, want remote host rejection", err)
	}
}

func TestInfoCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := NewClient("")
	ex := executor{local: true, run: func(context.Context, io.Reader, string, ...string) ([]byte, error) {
		cancel()
		return nil, errors.New("command killed")
	}}
	if _, err := c.Info(ctx, ex); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted Info error = %v, want context.Canceled", err)
	}
	if _, err := c.Info(ctx, executor{local: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Info error = %v, want context.Canceled", err)
	}
}
