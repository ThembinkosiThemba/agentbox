<h1 align="center">agentbox</h1>

<p align="center">
  <strong>See and manage everything your coding agents run.</strong><br>
  A terminal dashboard and CLI for the builds, tests, dev servers and background tasks started by Claude Code and other agents.
</p>

<p align="center">
  <a href="https://github.com/ThembinkosiThemba/agentbox/actions/workflows/ci.yml"><img src="https://github.com/ThembinkosiThemba/agentbox/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/ThembinkosiThemba/agentbox/releases/latest"><img src="https://img.shields.io/github/v/release/ThembinkosiThemba/agentbox?sort=semver" alt="Latest release"></a>
  <img src="https://img.shields.io/badge/platforms-windows%20%7C%20linux%20%7C%20macos-informational" alt="Platforms">
</p>

<p align="center">
  <img src="assets/agentbox.png" alt="agentbox showing a Claude Code session: every shell command it ran, with status, duration and output" width="900">
</p>

---

## Why

Coding agents run a lot of commands, often in the background, and you can't easily see:

- what is running right now, and which agent or session started it
- which command failed, and what it printed
- which dev server is still alive after the session that started it ended

Agents also hit hard time limits. Claude Code stops a shell command after 10 minutes, or after 30 minutes in the background, so long builds and test runs get killed partway through.

agentbox fixes both. It **watches** what your agents run, and gives them a way to **run long commands outside their shell tool**, where they keep going and you can still see and control them.

## Features

- **Live dashboard.** One screen for every run and every agent session on the machine, with status, durations, exit codes and live output.
- **Claude Code discovery, no setup.** Every Claude Code session from the last 7 days, with every shell command it ran (result, duration, output), its background tasks with live output, token use, cost and tool usage. Read-only: agentbox never writes to Claude's files.
- **Runs that outlive the agent.** `agentbox run -d` detaches from the caller, so the run survives the agent's tool timing out or its process tree being killed.
- **Full process-tree control.** Stopping a run stops everything it started: Job Objects on Windows, process groups on Linux and macOS.
- **Built for agents to call.** Short ids or names, flags anywhere, `--json` output, fixed exit codes, and `wait` with a timeout that stays under the agent's limit.
- **No daemon.** All state is plain files on disk, so the CLI and any number of dashboards can run at once.
- **A single static binary** for Windows, Linux and macOS, on x64 and ARM.

## Install

Download the binary for your platform from [**Releases**](https://github.com/ThembinkosiThemba/agentbox/releases/latest). It has no dependencies.

| Platform | x64 | ARM |
| --- | --- | --- |
| Windows | `agentbox-windows-amd64.exe` | `agentbox-windows-arm64.exe` |
| Linux | `agentbox-linux-amd64` | `agentbox-linux-arm64` |
| macOS | `agentbox-darwin-amd64` (Intel) | `agentbox-darwin-arm64` (Apple Silicon) |

**Linux / macOS**

```sh
os=$(uname -s | tr A-Z a-z); arch=$(uname -m)
[ "$arch" = x86_64 ] && arch=amd64; [ "$arch" = aarch64 ] && arch=arm64
mkdir -p ~/.local/bin
curl -fsSL -o ~/.local/bin/agentbox \
  "https://github.com/ThembinkosiThemba/agentbox/releases/latest/download/agentbox-$os-$arch"
chmod +x ~/.local/bin/agentbox
```

**Windows (PowerShell)**

```powershell
$dir = "$env:LOCALAPPDATA\Programs\agentbox"
New-Item -ItemType Directory -Force $dir | Out-Null
Invoke-WebRequest -OutFile "$dir\agentbox.exe" `
  https://github.com/ThembinkosiThemba/agentbox/releases/latest/download/agentbox-windows-amd64.exe
[Environment]::SetEnvironmentVariable("Path", "$([Environment]::GetEnvironmentVariable('Path','User'));$dir", "User")
# open a new terminal so PATH updates
```

Each release includes `checksums.txt` with the SHA-256 of every binary. On macOS, a binary downloaded through a browser is quarantined; clear it with `xattr -d com.apple.quarantine <file>`.

**From source** (Go 1.26+)

```sh
git clone https://github.com/ThembinkosiThemba/agentbox && cd agentbox
go build -o agentbox .
```

## Quick start

```sh
agentbox                                   # open the dashboard
agentbox run -d --name dev -- "npm run dev"  # start a detached run
agentbox ls                                # list runs
agentbox logs dev -f                       # follow its output
agentbox kill dev                          # stop it and everything it started
```

## Using it with coding agents

`agentbox guide` prints a short set of instructions for agents. Add them to the instructions file your agent reads:

```sh
agentbox guide >> ~/.claude/CLAUDE.md   # every Claude Code session on this machine
agentbox guide >> CLAUDE.md             # a single project
agentbox guide >> AGENTS.md             # Codex and other agents
```

From then on, the agent runs long commands through agentbox:

```sh
agentbox run -d --name build -- "npm run build"   # returns immediately with the run id
agentbox wait build --timeout 8m                   # exits with the command's own exit code
agentbox logs build -n 50                          # last 50 lines of output
```

If `wait` reaches its timeout, it exits `75` and the run keeps going. The agent calls `wait` again rather than starting the command over. Each run records which agent and session launched it. Claude Code is detected automatically; other agents can set `AGENTBOX_AGENT=<name>`.

## The dashboard

Run `agentbox` with no arguments.

**Runs tab**: every command started through agentbox, running first. The side pane shows the command, working directory, who launched it, timing, and a live tail of the output.

**Claude tab**: Claude Code sessions, active first. Each one shows project, branch, model, usage, cost, tool usage, background tasks and recent commands. Open a session to browse every command it ran, with the full command and its result, and every background task with live output.

| Key | Action |
| --- | --- |
| `←` `→` · `Tab` · click | Switch between Runs and Claude |
| `↑` `↓` · `j` `k` · click | Move the selection |
| `Enter` | Open: full-screen log, session details, task output |
| `Esc` | Back, or clear the filter |
| `/` | Filter the current list |
| `n` | Start a new run in the current directory |
| `x` | Stop a run and its whole process tree |
| `r` | Restart a run |
| `d` | Delete a finished run and its log |
| `f` · `w` | In a log: follow output · wrap lines |
| `?` | Show all keys |
| `q` | Quit |

## CLI reference

| Command | Description |
| --- | --- |
| `agentbox run [flags] -- <command…>` | Run a command. `-d` detach · `-n` name · `-t 30m` timeout · `-C dir` · `-q` print id only · `--json` |
| `agentbox ls [-a] [--json]` | List runs: running ones plus the 20 most recent (`-a` for all) |
| `agentbox status <id> [--json]` | Show one run |
| `agentbox logs <id> [-f] [-n N]` | Print output; `-f` follows until the run ends |
| `agentbox wait <id> [-t 8m] [-n 20]` | Block until the run ends, then print its last lines |
| `agentbox kill <id>…` | Stop runs and their process trees |
| `agentbox restart <id>` | Run the same command again, detached |
| `agentbox rm [-f] <id>…` | Delete runs |
| `agentbox prune [--older-than 24h]` | Delete finished runs |
| `agentbox sessions [id] [--days 3] [--json]` | List Claude Code sessions, or show one session's tasks and commands |
| `agentbox guide` | Print the instructions for agents |
| `agentbox version` | Print the version |

Anywhere a command takes `<id>`, you can use the full id, any unique prefix of it, or a run's `--name` (the newest run with that name).

**How commands are run.** A single argument goes through the shell, so pipes, `&&` and variables work: `agentbox run -- "npm ci && npm test"`. Several arguments are run directly, without a shell: `agentbox run -- go test ./...`. Use `--shell` or `--no-shell` to override this.

Which shell is used:
- **Windows:** Git Bash when agentbox is launched from it (as Claude Code does), otherwise `cmd`.
- **Linux and macOS:** `$SHELL`.
- **Override:** set `AGENTBOX_SHELL`, e.g. `AGENTBOX_SHELL="pwsh -NoProfile -Command"`.

### Exit codes

| Code | Meaning |
| --- | --- |
| *the command's own code* | `run` (foreground) and `wait`, when the command exited by itself |
| `75` | `wait --timeout` expired; the run is still going |
| `124` | The run hit its own `--timeout` |
| `127` | The command could not be started |
| `130` | A foreground run was stopped with Ctrl+C |
| `137` | The run was stopped |

## How it works

agentbox has no daemon. Each run is a directory on disk:

| OS | Location |
| --- | --- |
| Windows | `%LOCALAPPDATA%\agentbox\runs\<id>\` |
| Linux / macOS | `~/.local/state/agentbox/runs/<id>/` (respects `$XDG_STATE_HOME`) |

Set `AGENTBOX_HOME` to use another location. Each run directory holds `meta.json`, `output.log` and a `heartbeat` file.

1. `agentbox run -d` starts a small **supervisor** process (agentbox itself) detached from the caller:
   - **Windows:** no console window, its own process group, and outside the caller's job object where the job allows it.
   - **Linux and macOS:** a new session.

   The caller can exit or be killed without affecting the run.
2. The supervisor starts the command in its own **process tree**: a named Job Object on Windows, a process group elsewhere. Output is written to `output.log`.
3. Stopping a run sends Ctrl+Break or SIGTERM to the whole tree, then force-kills it after 5 seconds. When the command exits, anything it left running is cleaned up too.
4. The supervisor writes a heartbeat every 2 seconds. If a run says it is running but its heartbeat is more than 15 seconds old, it is shown as **lost**. That happens when the supervisor died, for example after a reboot.

Claude Code sessions are read from `~/.claude/projects` (or `$CLAUDE_CONFIG_DIR`). Transcripts are parsed incrementally, so refreshing stays fast even with long sessions.

## Development

```sh
go build -o agentbox .
go test ./...
AGENTBOX_HOME=/tmp/agentbox-dev ./agentbox run -d -- "sleep 5"   # keep test runs separate
```

The code layout and the rules to keep (no daemon, read-only Claude access, terminal-width-safe rendering) are in [AGENTS.md](AGENTS.md). CI runs the tests on Linux, Windows and macOS.

### Releasing

Push a version tag:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

The [release workflow](.github/workflows/release.yml) runs the tests on all three platforms, builds all six binaries with the version stamped in, and publishes them with checksums to a GitHub release.
