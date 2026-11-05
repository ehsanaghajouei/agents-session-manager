// Command asm is a terminal UI for managing Claude Code sessions.
package main

import (
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
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider/claude"
	"github.com/ehsanaghajouei/agents-session-manager/internal/tmux"
	"github.com/ehsanaghajouei/agents-session-manager/internal/tui"
	"github.com/ehsanaghajouei/agents-session-manager/internal/watch"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "asm:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "config file (default $XDG_CONFIG_HOME/asm/config.toml)")
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
	if fi, err := os.Stat(cfg.ClaudeDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("Claude data directory %s not found; set claude_dir in the config", cfg.ClaudeDir)
	}

	cacheDir, err := config.CacheDir()
	if err != nil {
		return err
	}
	cache := index.Open(filepath.Join(cacheDir, "index.json"))

	ex := host.Local{}
	prov := claude.New(cfg.ClaudeDir, cfg.ClaudePath, cfg.ClaudeArgs, cache)
	mgr := manager.New(ex, prov, tmux.New(ex, ""), cfg.TmuxPrefix)

	var warnings []string
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
	pinStore, err := idset.Load(filepath.Join(configDir, "pins.json"), "pins")
	if err != nil {
		warnings = append(warnings, "pinning disabled: "+err.Error())
	}
	nameStore, err := names.Load(filepath.Join(configDir, "names.json"))
	if err != nil {
		warnings = append(warnings, "renaming disabled: "+err.Error())
	}
	hiddenSet, err := idset.Load(filepath.Join(configDir, "hidden.json"), "hidden")
	if err != nil {
		warnings = append(warnings, "hiding disabled: "+err.Error())
	}

	var changes <-chan struct{}
	w, err := watch.New(time.Second, []string{prov.SessionsDir()}, []string{prov.ProjectsDir()})
	if err != nil {
		warnings = append(warnings, "file watching disabled: "+err.Error())
	} else {
		defer w.Close()
		changes = w.C
	}

	m := tui.New(tui.Options{
		Manager:   mgr,
		Config:    cfg,
		Changes:   changes,
		AfterScan: cache.Save,
		Warnings:  warnings,
		Pins:      pinStore,
		Names:     nameStore,
		Hidden:    hiddenSet,
	})
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return err
	}
	return cache.Save()
}
