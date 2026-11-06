# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Project

`asm` is a Bubble Tea TUI for Claude Code sessions. It lists saved and running sessions and resumes or attaches each one in its own tmux session, named `<tmux_prefix><session-id>` (default `cc-`). It runs on Linux and macOS: `internal/proc` reads `/proc` on Linux and uses sysctl and `ps` on macOS.

User-facing behavior is documented in `README.md`. Keep it in sync when you change keys, states, filters, config or file paths.

## Commands

```sh
go build ./...
go vet ./...
GOOS=darwin go vet ./...      # the macOS code is not compiled on Linux otherwise
go test ./...
gofmt -l .                    # must print nothing
go build -o asm ./cmd/asm
sh -n install.sh
```

## Layout

| Package | Role |
|---|---|
| `cmd/asm` | Entry point: flags, config, wiring, starts the Bubble Tea program. |
| `internal/config` | TOML config, defaults, config/cache dirs (XDG on Linux, `~/Library` on macOS). |
| `internal/host` | `Executor` interface (`Run`, `Interactive`); `Local` implementation. Every external command goes through it. |
| `internal/proc` | Process helpers: start time as Claude records it, liveness with pid-reuse guard, ancestry, SIGTERM-and-wait. `/proc` on Linux (`proc_linux.go`), sysctl and `ps` on macOS (`proc_darwin.go`). |
| `internal/tmux` | tmux client: list sessions and pane pids, new/kill/attach, `capture-pane`, prefix lookup. |
| `internal/provider` | Agent-neutral types (`Session`, `Process`, `Transcript`) and the `Provider` interface. |
| `internal/provider/claude` | Claude Code parsers (transcripts, live-process files), resume/new commands, and session backups (`backup.go`). |
| `internal/index` | JSON cache of parsed transcript metadata, keyed by file path with size, mtime and offset. |
| `internal/manager` | Combines sessions, processes and tmux sessions into `Item`s with a `State`; resume, new, kill and terminate. |
| `internal/watch` | fsnotify watcher with debounce; signals the TUI to rescan. |
| `internal/jsonfile` | Reads and atomically writes asm's JSON state files. |
| `internal/idset` | A set of sessions in a JSON file: pins (`pins.json`) and hidden sessions (`hidden.json`) in the config directory. |
| `internal/names` | Names the user gave sessions, stored in `names.json` in the config directory. The TUI copies them into `manager.Item.Alias`. |
| `internal/tui` | Bubble Tea model: list, preview, filter, dialogs, help. |

Adding another agent means a new package under `internal/provider/` that implements `provider.Provider`. The manager and the TUI must not depend on Claude-specific details.

## Claude Code data (internal, undocumented formats)

- **Transcripts:** `<claude_dir>/projects/<encoded-cwd>/<session-id>.jsonl`.
  - Subagent transcripts live in deeper directories and are ignored.
  - Some lines are several MB long. Read with `bufio.Reader.ReadBytes`, not `bufio.Scanner`.
  - Check a line's `"type"` prefix before decoding it, and skip records that aren't needed.
  - Files are append-only. Parse incrementally from the cached offset, and leave a partial last line for the next scan.
- **Running processes:** `<claude_dir>/sessions/<pid>.json`.
  - Fields: `pid`, `sessionId`, `cwd`, `status`, `name`, `procStart`, `updatedAt` (ms).
  - `procStart` is field 22 of `/proc/<pid>/stat` on Linux, and the output of `LC_ALL=C TZ=UTC ps -o lstart= -p <pid>`, trimmed, on macOS.
  - Always compare `procStart` with the live process to guard against pid reuse.
- Parsers must tolerate unknown fields and record types.

## Rules

- **Read-only access to Claude Code's files.** Never write, move or delete anything under `claude_dir`. The one exception is importing a backup (`provider/claude/backup.go`), which runs only after the user confirms:
  - It unpacks into a staging directory inside `claude_dir` and validates every archive entry before moving anything into place.
  - It never leaves two transcripts with the same session ID, because `claude --resume <id>` refuses an ID it finds in two projects.
- **No second process on one transcript.**
  - Never start a second Claude process for a session that is already running.
  - `Manager.Resume` re-checks the state at call time and returns `*ExternalError` for a process running outside tmux.
  - The TUI then asks before sending SIGTERM.
- **Confirmation:** every destructive action (kill, terminate, hide) asks first.
- **tmux targets:**
  - Use exact-match targets (`=name`).
  - Start commands with an argv list, never through a shell string.
  - Treat "no server running" as an empty session list, not as an error.
- **Bubble Tea state:**
  - Change model state only in `Update`.
  - Background work runs in a `tea.Cmd` and returns a message.
- **Rendered size:** the view must fit the terminal exactly. `TestViewFitsTerminal` checks several sizes.
  - When you add footer keys, extend the drop order in `viewFooter`.
  - Wide help lines are truncated.
- **Adding a key:**
  - Add the binding in `internal/tui/keys.go`.
  - Add it to `FullHelp`.
  - Add it to `ShortHelp` only if it belongs in the footer.
  - Update the keys table in `README.md`.
- **Libraries:**
  - Bubble Tea v2 import paths are `charm.land/bubbletea/v2`, `charm.land/bubbles/v2` and `charm.land/lipgloss/v2`.
  - Bubble Tea v2 `View()` returns `tea.View`.

## Testing

- Tests must never touch the real `~/.claude`, real Claude processes, or the user's tmux server.
  - Use fixtures in `t.TempDir()`.
  - Use fake `host.Executor` and `provider.Provider` implementations (see `manager_test.go` and `tui/model_test.go`).
  - Tests that need real tmux use a private socket (`tmux -L asm-test-<pid>`) and kill that server in cleanup.
- For manual runs, point `XDG_CONFIG_HOME` and `XDG_CACHE_HOME` at a temporary directory, so the real config, pins and cache are left alone. macOS ignores these variables; set `HOME` instead.
- To test resume or new-session flows by hand, set `claude_path` in a temporary config to a fake script.
- `internal/proc` tests run against the real OS. The release workflow runs `go test` on Linux and macOS.

## Releases and install script

- Pushing a `v*` tag runs `.github/workflows/release.yml`. It tests, then builds `asm_<os>_<arch>.tar.gz` (linux and darwin, amd64 and arm64) and `checksums.txt`, and publishes a GitHub release. The version comes from the tag through `-X main.version`.
- `install.sh` downloads those files by name from the latest release. Keep the archive names and contents in sync between the two files.
- `install.sh` must stay POSIX `sh`: it runs as `curl … | sh` under dash, bash and macOS `sh`.
- To test `install.sh` without a release, build the archives as the workflow does, serve them over local HTTP, point a copy of the script at that URL, and set `HOME` to a temporary directory. Use a `PATH` with fake `sudo` and package-manager scripts to test the tmux install.
