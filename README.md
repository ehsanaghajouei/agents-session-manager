# asm: agents session manager

`asm` is a terminal UI for managing [Claude Code](https://claude.com/claude-code) sessions. It lists every session on the machine, both saved ones and running ones, and opens any of them inside tmux.

- Each Claude session runs in its own tmux session, named `cc-<session-id>`.
- **Enter** suspends `asm` and attaches to that tmux session. Detach with the tmux prefix and **d** to return to `asm`; Claude keeps running.
- `asm` only reads Claude Code's files. It never edits or deletes them.

## Features

- **Session list**
  - Columns: status, host, title, project directory, git branch, last activity, cost and short ID.
  - The title is Claude's generated title, or the first prompt when there is none.
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
- **Actions:**
  - Resume or attach.
  - Start a new session in a directory.
  - Kill a session's tmux session.
  - Copy the session ID or the resume command to the clipboard. This uses OSC 52, so it also works over SSH.
- **Safety:**
  - A session already running outside tmux is never resumed a second time, because two processes would write the same transcript. `asm` offers to stop that process first, and asks for confirmation.
  - Destructive actions always ask for confirmation.

## Requirements

- Linux. Process detection reads `/proc`.
- [tmux](https://github.com/tmux/tmux)
- The [Claude Code](https://claude.com/claude-code) CLI (`claude`) in `PATH`.
- Go 1.27 or newer, to build.

## Install

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

Plain words are fuzzy-matched against the title, directory and session ID. Every word must match.

```
status:busy,idle   busy | idle | tmux | external (ext) | saved | running
project:infra      directory contains "infra"           (alias p:)
host:local         host contains "local"                (alias h:)
since:7d           active in the last 30m / 12h / 7d / 2w, or since 2026-10-01
until:2026-10-01   active up to and including that day, or before 7d ago
```

`is:` is an alias for `status:`.

## Configuration

`asm` reads `$XDG_CONFIG_HOME/asm/config.toml` (default `~/.config/asm/config.toml`). The file is optional. Every setting and its default:

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
```

## Files

| Path | Contents |
|---|---|
| `$XDG_CONFIG_HOME/asm/config.toml` | Settings (optional). |
| `$XDG_CONFIG_HOME/asm/pins.json` | Pinned sessions. |
| `$XDG_CACHE_HOME/asm/index.json` | Cache of parsed transcript metadata. It is safe to delete. |

## How it works

- **Saved sessions:** read from `<claude_dir>/projects/*/<session-id>.jsonl`.
  - Each file is parsed once and cached.
  - Later scans parse only the lines appended since the last scan.
- **Running sessions:** read from `<claude_dir>/sessions/<pid>.json`.
  - The process start time is checked, so a reused pid is not mistaken for a live session.
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

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE).
