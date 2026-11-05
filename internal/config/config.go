// Package config loads asm settings from ~/.config/asm/config.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const appName = "asm"

// Config holds user settings. Zero values are replaced by defaults on load.
type Config struct {
	// ClaudeDir is Claude Code's data directory (projects/, sessions/).
	ClaudeDir string `toml:"claude_dir"`
	// ClaudePath is the claude binary, looked up in PATH when not absolute.
	ClaudePath string `toml:"claude_path"`
	// ClaudeArgs are extra arguments added to every resume/new command.
	ClaudeArgs []string `toml:"claude_args"`
	// TmuxPrefix prefixes tmux session names: <prefix><session-id>.
	TmuxPrefix string `toml:"tmux_prefix"`
	// RefreshInterval is how often running state is polled.
	RefreshInterval Duration `toml:"refresh_interval"`
	// PreviewMessages is how many recent messages the preview shows.
	PreviewMessages int `toml:"preview_messages"`
}

// Duration is a time.Duration that reads from TOML strings like "5s".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// Default returns the built-in settings.
func Default() Config {
	return Config{
		ClaudeDir:       defaultClaudeDir(),
		ClaudePath:      "claude",
		TmuxPrefix:      "cc-",
		RefreshInterval: Duration{5 * time.Second},
		PreviewMessages: 20,
	}
}

// Load reads path, or the default config file when path is empty. A missing
// default file is not an error.
func Load(path string) (Config, error) {
	cfg := Default()
	explicit := path != ""
	if !explicit {
		dir, err := ConfigDir()
		if err != nil {
			return cfg, err
		}
		path = filepath.Join(dir, "config.toml")
	}

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist) && !explicit:
		return cfg, nil
	case err != nil:
		return cfg, err
	}

	var file Config
	if err := toml.Unmarshal(data, &file); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	merge(&cfg, file)

	cfg.ClaudeDir = expandHome(cfg.ClaudeDir)
	return cfg, cfg.validate()
}

func merge(dst *Config, src Config) {
	if src.ClaudeDir != "" {
		dst.ClaudeDir = src.ClaudeDir
	}
	if src.ClaudePath != "" {
		dst.ClaudePath = src.ClaudePath
	}
	if src.ClaudeArgs != nil {
		dst.ClaudeArgs = src.ClaudeArgs
	}
	if src.TmuxPrefix != "" {
		dst.TmuxPrefix = src.TmuxPrefix
	}
	if src.RefreshInterval.Duration > 0 {
		dst.RefreshInterval = src.RefreshInterval
	}
	if src.PreviewMessages > 0 {
		dst.PreviewMessages = src.PreviewMessages
	}
}

func (c Config) validate() error {
	// tmux uses '.' and ':' as target separators.
	if strings.ContainsAny(c.TmuxPrefix, ".: ") {
		return fmt.Errorf("tmux_prefix %q must not contain '.', ':' or spaces", c.TmuxPrefix)
	}
	if c.RefreshInterval.Duration < time.Second {
		return fmt.Errorf("refresh_interval must be at least 1s")
	}
	return nil
}

// ConfigDir is $XDG_CONFIG_HOME/asm (default ~/.config/asm).
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appName), nil
}

// CacheDir is $XDG_CACHE_HOME/asm (default ~/.cache/asm).
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appName), nil
}

// defaultClaudeDir follows Claude Code: $CLAUDE_CONFIG_DIR, else ~/.claude.
func defaultClaudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return expandHome("~/.claude")
}

func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}
