// Command asm is a terminal UI for browsing and managing agent sessions.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ehsanaghajouei/agents-session-manager/internal/config"
	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/idset"
	"github.com/ehsanaghajouei/agents-session-manager/internal/index"
	"github.com/ehsanaghajouei/agents-session-manager/internal/manager"
	"github.com/ehsanaghajouei/agents-session-manager/internal/names"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider/claude"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider/opencode"
	"github.com/ehsanaghajouei/agents-session-manager/internal/tmux"
	"github.com/ehsanaghajouei/agents-session-manager/internal/tui"
	"github.com/ehsanaghajouei/agents-session-manager/internal/watch"
)

var version = "dev"

const openCodeTmuxPrefix = "oc-"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "asm:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "config file (default config.toml in the asm config directory)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("asm", version)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	cacheDir, err := config.CacheDir()
	if err != nil {
		return err
	}
	cache := index.Open(filepath.Join(cacheDir, "index.json"))

	ex := host.Local{}
	tm := tmux.New(ex, "")
	prov := claude.New(cfg.ClaudeDir, cfg.ClaudePath, cfg.ClaudeArgs, cache)
	mgr := manager.New(ex, prov, tm, cfg.TmuxPrefix)
	openCode := opencode.NewClient("")

	var warnings []string
	if fi, err := os.Stat(cfg.ClaudeDir); err != nil || !fi.IsDir() {
		warnings = append(warnings, fmt.Sprintf("Claude data directory %s not found; set claude_dir in the config", cfg.ClaudeDir))
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		warnings = append(warnings, "tmux not found in PATH; resume and new sessions need it")
	}
	if _, err := exec.LookPath(cfg.ClaudePath); err != nil {
		warnings = append(warnings, cfg.ClaudePath+" not found; set claude_path in the config")
	}

	configDir, err := config.ConfigDir()
	if err != nil {
		return err
	}
	claudePrefs, claudeWarnings := loadPreferences(configDir)
	warnings = append(warnings, claudeWarnings...)
	openCodePrefs, openCodeWarnings := loadPreferences(filepath.Join(configDir, "opencode"))

	var changes <-chan struct{}
	w, err := watch.New(time.Second, []string{prov.SessionsDir()}, []string{prov.ProjectsDir()})
	if err != nil {
		warnings = append(warnings, "file watching disabled: "+err.Error())
	} else {
		defer w.Close()
		changes = w.C
	}

	m := tui.New(tui.Options{
		Manager: mgr,
		ScanOpenCode: func(ctx context.Context) (manager.Snapshot, error) {
			return manager.ScanService(ctx, ex, openCode, tm, openCodeTmuxPrefix)
		},
		LoadOpenCode: func(ctx context.Context, s provider.Session, n int) (provider.Transcript, error) {
			return openCode.Load(ctx, ex, s, n)
		},
		ResumeOpenCode: func(ctx context.Context, s provider.Session) (*exec.Cmd, error) {
			return manager.ResumeService(ctx, ex, openCode, tm, openCodeTmuxPrefix, s.ID)
		},
		NewOpenCode: func(ctx context.Context, dir string) (string, *exec.Cmd, error) {
			return manager.NewService(ctx, ex, openCode, tm, openCodeTmuxPrefix, dir)
		},
		Config:              cfg,
		Changes:             changes,
		AfterScan:           cache.Save,
		Warnings:            warnings,
		OpenCodeWarnings:    openCodeWarnings,
		ClaudePreferences:   claudePrefs,
		OpenCodePreferences: openCodePrefs,
	})
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return err
	}
	return cache.Save()
}

func loadPreferences(dir string) (tui.Preferences, []string) {
	var warnings []string
	pins, err := idset.Load(filepath.Join(dir, "pins.json"), "pins")
	if err != nil {
		warnings = append(warnings, "pinning disabled: "+err.Error())
	}
	sessionNames, err := names.Load(filepath.Join(dir, "names.json"))
	if err != nil {
		warnings = append(warnings, "renaming disabled: "+err.Error())
	}
	hidden, err := idset.Load(filepath.Join(dir, "hidden.json"), "hidden")
	if err != nil {
		warnings = append(warnings, "hiding disabled: "+err.Error())
	}
	return tui.Preferences{Pins: pins, Names: sessionNames, Hidden: hidden}, warnings
}
