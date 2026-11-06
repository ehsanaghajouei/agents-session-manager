# asm: agents session manager

`asm` is a terminal UI for managing [Claude Code](https://claude.com/claude-code) sessions. It lists every session on the machine, both saved ones and running ones, and opens any of them inside tmux.

- Each Claude session runs in its own tmux session, named `cc-<session-id>`.
- **Enter** suspends `asm` and attaches to that tmux session. Detach with the tmux prefix and **d** to return to `asm`; Claude keeps running.
- `asm` only reads Claude Code's files, except when you import a session backup.

## Features

- **Session list**
  - Columns: status, host, title, project directory, git branch, last activity, cost and short ID.
  - The title is the name you gave the session in `asm`; otherwise Claude's generated title, or the first prompt when there is none.
- **Live status**
  - Updates automatically when Claude's files change, and polls for running processes.
  - Status values:

    | Status | Meaning |
    |---|---|
    | `● busy` | Running in its tmux session, working. |
    | `● idle` | Running in its tmux session, waiting for input. |
    | `◆ tmux` | The tmux session exists, but Claude has not reported a process yet. |
    | `▲ external` | Running outside tmux, because it was started elsewhere. `asm` cannot attach to it. |
    | `○ saved` | Not running. Enter resumes it in a new tmux session. |

- **Preview pane**
  - Shows either a live capture of the session's tmux pane, or the recent transcript rendered as markdown.
  - Also shows metadata: directory, branch, model, cost, tokens and lines changed.
  - It is hidden on terminals narrower than 100 columns.
- **Sort:** by activity, cost, project or status.
- **Group:** by last activity (Today, Yesterday, then one group per date), by project, by host, or no grouping.
- **Filter:** fuzzy text plus field filters for status, project, host and date range.
- **Pins:** pinned sessions stay in a group at the top of the list.
- **Rename:** give a session a name that `asm` shows instead of Claude's title. The name is stored by `asm` only; Claude Code does not see it.
- **Hide:** remove a session from the list without touching its files. `U` shows hidden sessions again, dimmed. Hiding a running session stops it first.
- **Move sessions between machines:** export a session to a `.tar.gz` file and import it on another machine. See [Moving a session to another machine](#moving-a-session-to-another-machine).
- **Actions:**
  - Resume or attach.
  - Start a new session in a directory.
  - Kill a session's tmux session.
  - Copy the session ID or the resume command to the clipboard. This uses OSC 52, so it also works over SSH.
- **Safety:**
  - A session already running outside tmux is never resumed a second time, because two processes would write the same transcript. `asm` offers to stop that process first, and asks for confirmation.
  - Destructive actions always ask for confirmation.

## Requirements

- Linux or macOS, on amd64 or arm64.
- [tmux](https://github.com/tmux/tmux)
- The [Claude Code](https://claude.com/claude-code) CLI (`claude`) in `PATH`.
- Go 1.27 or newer, only to build from source.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/ehsanaghajouei/agents-session-manager/main/install.sh | sh
```

The script:

- Downloads the latest release for your OS and CPU, checks its SHA-256 checksum, and installs `asm` to `~/.local/bin`.
- Installs tmux when it is missing: with apt, dnf, yum, pacman, zypper or apk on Linux (using `sudo` unless run as root), and with Homebrew on macOS.
- Warns when `claude` is not in `PATH`, or when `~/.local/bin` is not in `PATH`.

Run it again to upgrade.

With Go:

```sh
go install github.com/ehsanaghajouei/agents-session-manager/cmd/asm@latest
```

Or build from source:

```sh
git clone git@github.com:ehsanaghajouei/agents-session-manager.git
cd agents-session-manager
go build -o asm ./cmd/asm
```

## Usage

```sh
asm                  # start the TUI
asm -config FILE     # use another config file
asm -version         # print the version
```

On start, sessions are grouped by last activity and sorted by activity, and the preview shows the live tmux pane. Press `?` in the app for all keys, the status legend and the filter syntax.

### Keys

| Key | Action |
|---|---|
| `↑`/`k`, `↓`/`j` | Move up / down |
| `pgup`/`ctrl+b`, `pgdn`/`ctrl+f` | Page up / down |
| `g`, `G` | Top / bottom |
| `enter` | Resume or attach |
| `n` | New session (asks for a directory) |
| `x` | Kill the session's tmux session |
| `P` | Pin / unpin |
| `R` | Rename (an empty name shows Claude's title again) |
| `H` | Hide / unhide (asks first; a running session is stopped before it is hidden) |
| `U` | Show or stop showing hidden sessions |
| `E` | Export the session to a backup file |
| `I` | Import a backup file |
| `y` | Copy the session ID |
| `Y` | Copy the resume command (`cd <dir> && claude --resume <id>`) |
| `/` | Filter |
| `esc` | Clear the filter |
| `s` | Change sort: activity → cost → project → status |
| `S` | Change grouping: last → none → project → host |
| `tab` | Switch the preview between live pane and transcript |
| `p` | Show / hide the preview |
| `J`, `K` | Scroll the preview |
| `ctrl+d`, `ctrl+u` | Scroll the preview half a page |
| `r` | Refresh |
| `?` | Help |
| `q`, `ctrl+c` | Quit |

Inside a session, press the tmux prefix and then `d` (by default `C-b d`) to return to `asm`.

### Filter syntax

Plain words are fuzzy-matched against the name, title, directory and session ID. Every word must match.

```
status:busy,idle   busy | idle | tmux | external (ext) | saved | running
project:infra      directory contains "infra"           (alias p:)
host:local         host contains "local"                (alias h:)
since:7d           active in the last 30m / 12h / 7d / 2w, or since 2026-10-01
until:2026-10-01   active up to and including that day, or before 7d ago
```

`is:` is an alias for `status:`.

## Configuration

`asm` reads `config.toml` from its config directory. The file is optional.

- Linux: `$XDG_CONFIG_HOME/asm/` (default `~/.config/asm/`)
- macOS: `~/Library/Application Support/asm/`

Every setting and its default:

```toml
# Claude Code's data directory. Default: $CLAUDE_CONFIG_DIR, else ~/.claude
claude_dir = "~/.claude"

# The claude binary; looked up in PATH when not absolute.
claude_path = "claude"

# Extra arguments added to every resume and new-session command.
claude_args = []

# Prefix for tmux session names: <prefix><session-id>.
# Must not contain '.', ':' or spaces.
tmux_prefix = "cc-"

# How often running state is polled. Minimum 1s.
refresh_interval = "5s"

# How many recent messages the transcript preview shows.
preview_messages = 20

# Where E writes backups by default.
backup_dir = "~/asm-backups"
```

## Files

`<config>` is the config directory above. `<cache>` is `$XDG_CACHE_HOME/asm/` on Linux (default `~/.cache/asm/`) and `~/Library/Caches/asm/` on macOS.

| Path | Contents |
|---|---|
| `<config>/config.toml` | Settings (optional). |
| `<config>/pins.json` | Pinned sessions. |
| `<config>/names.json` | Names given to sessions. |
| `<config>/hidden.json` | Hidden sessions. |
| `<cache>/index.json` | Cache of parsed transcript metadata. It is safe to delete. |

## Moving a session to another machine

1. On the first machine, select the session and press `E`. `asm` writes `<backup_dir>/<session-id>.tar.gz`; you can edit the path first. The backup holds:
   - the transcript
   - its subagent transcripts and tool results
   - Claude's file history (the checkpoints that undo Claude's edits)
2. Copy the file to the other machine, for example with `scp`.
3. On the other machine, press `I` and enter the backup's path. Then enter the project's directory on that machine, which must exist.
   - If the directory differs from the original, `asm` changes the transcript's working-directory fields to the new one. File paths inside messages and tool output still show the original machine's paths.
   - If a session with the same ID already exists there, `asm` warns you and replaces it on confirmation. It refuses while that session is running.
   - If that machine's `claude` is older than the version that wrote the session, `asm` warns that the session may not load.

Claude Code's transcript format is internal and can change between versions, so a newer Claude on the target machine is the safer direction. Your pins, names and hidden sessions are not part of the backup.

## How it works

- **Saved sessions:** read from `<claude_dir>/projects/*/<session-id>.jsonl`.
  - Each file is parsed once and cached.
  - Later scans parse only the lines appended since the last scan.
- **Running sessions:** read from `<claude_dir>/sessions/<pid>.json`.
  - The process start time is checked, so a reused pid is not mistaken for a live session. It comes from `/proc` on Linux and from `ps` on macOS, the same sources Claude Code uses.
  - A session counts as inside tmux only if its process is a descendant of a pane in its own `cc-<id>` tmux session. Otherwise it is `external`.
- **Resume:** runs `claude --resume <id>` with tmux `new-session`, in the session's original directory. No shell is involved.
- **New session:** starts `claude --session-id <uuid>`, so the tmux session name is known before Claude starts.

These Claude Code file formats are internal and undocumented. A Claude Code update can change them and break parts of `asm`.

## Development

```sh
go build ./...
go vet ./...
go test ./...
```

The tmux tests start their own tmux server on a private socket and skip when tmux is not installed.

### Releases

Pushing a tag such as `v0.1.0` runs `.github/workflows/release.yml`:

1. Runs the tests on Linux and macOS.
2. Builds `asm_<os>_<arch>.tar.gz` for `linux` and `darwin`, each for `amd64` and `arm64`, plus `checksums.txt`.
3. Publishes them as a GitHub release.

`install.sh` downloads these files by name.

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE).
