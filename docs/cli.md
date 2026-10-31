# CLI reference

```
agentbox                  open the dashboard
agentbox <command> ...
```

Anywhere a command takes `<id>`, you can use the full id, any unique prefix of it, or a run's `--name` (the newest run with that name). Flags can come before or after positional arguments. Output has no colors when it isn't going to a terminal.

## run

```
agentbox run [flags] -- <command...>
```

Runs a command under agentbox. Without `-d` it runs in the foreground: output streams to your terminal and is also saved, and `agentbox` exits with the command's exit code. The run id is printed to stderr.

| Flag | Description |
| --- | --- |
| `-d`, `--detach` | Start in the background and return immediately |
| `-n`, `--name <name>` | A name to refer to the run by |
| `--title <text>` | What the run is for, in plain words; shown in the dashboard and `ls` instead of the command |
| `-t`, `--timeout <duration>` | Stop the run after this long, e.g. `30m`, `2h` |
| `-C`, `--cwd <dir>` | Working directory (default: current) |
| `-s`, `--shell` | Run through the shell even with several arguments |
| `--no-shell` | Run directly, without a shell, even with a single argument |
| `-q`, `--quiet` | With `-d`, print only the run id |
| `--json` | With `-d`, print the run as JSON |

**Shell or not.** A single argument goes through the shell, so pipes, `&&`, globs and variables work:

```sh
agentbox run -d -- "npm ci && npm test 2>&1 | tee test.log"
```

Several arguments are run directly, without a shell:

```sh
agentbox run -d -- go test ./...
```

**Which shell:**

| Platform | Shell |
| --- | --- |
| Windows | Git Bash when agentbox was started from it (as Claude Code does), otherwise `cmd` |
| Linux, macOS | `$SHELL`, falling back to `/bin/sh` |
| Any | `AGENTBOX_SHELL` overrides, e.g. `AGENTBOX_SHELL="pwsh -NoProfile -Command"` |

## ls

```
agentbox ls [-a] [--json]
```

Lists running runs plus the 20 most recent. Aliases: `ps`, `list`.

| Flag | Description |
| --- | --- |
| `-a`, `--all` | List every run |
| `--json` | Print JSON |

## status

```
agentbox status <id> [--json]
```

Shows one run: status, command, directory, times, exit code, pid, agent, timeout, error and log path. Alias: `show`.

## logs

```
agentbox logs <id> [-f] [-n N]
```

Prints a run's output (stdout and stderr together). Alias: `log`.

| Flag | Description |
| --- | --- |
| `-f`, `--follow` | Keep printing new output until the run ends |
| `-n`, `--lines <N>` | Print only the last N lines |

## wait

```
agentbox wait <id> [--timeout 8m] [-n 20]
```

Blocks until the run ends, prints its last lines, and exits with the run's [exit code](#exit-codes).

| Flag | Description |
| --- | --- |
| `-t`, `--timeout <duration>` | Stop waiting after this long and exit `75`; the run keeps going |
| `-n`, `--lines <N>` | Lines of output to print when the run ends (default 20, `0` for none) |

## kill

```
agentbox kill <id>...
```

Stops runs and every process they started. Alias: `stop`. The command first gets Ctrl+Break (Windows) or SIGTERM (Linux, macOS), and is force-killed if it is still running after 5 seconds.

## restart

```
agentbox restart <id> [-q]
```

Starts the run's command again as a new detached run, with the same name, directory and timeout. If the old run is still going, it is stopped first.

## rm and prune

```
agentbox rm [-f] <id>...
agentbox prune [--older-than 24h]
```

`rm` deletes runs and their logs. It refuses to delete a run that is still going unless you pass `-f`, which stops it first. `prune` deletes all finished runs, or only those that finished longer ago than `--older-than`.

## sessions

```
agentbox sessions [id] [--days 3] [--json]
```

Lists Claude Code sessions active within `--days` (default 3): active state, id, last activity, number of commands, background tasks, project and title. Give a session id (or a prefix of it) to list that session's background tasks and recent commands instead.

## guide

```
agentbox guide
```

Prints instructions for coding agents, ready to add to `CLAUDE.md` or `AGENTS.md`. See [Using agentbox with coding agents](agents.md).

## shortcut

```
agentbox shortcut [--desktop] [--remove]
```

Adds agentbox to your system's app launcher, so the dashboard opens like any other app and can be pinned:

| System | What it adds |
| --- | --- |
| Windows | An **agentbox** Start menu shortcut with the agentbox icon. Right-click it and choose **Pin to taskbar**. `--desktop` adds one to the desktop too |
| Linux | `~/.local/share/applications/agentbox.desktop` and its icon; it opens in your terminal |
| macOS | `~/Applications/agentbox.command`, which opens agentbox in Terminal; drag it to the Dock |

The launcher points at the `agentbox` binary you ran the command with. The install scripts run it for you. `--remove` deletes what it added.

## version

```
agentbox version
```

## Exit codes

| Code | Meaning |
| --- | --- |
| *the command's own code* | `run` (foreground) and `wait`, when the command exited by itself |
| `0` | Success; for `kill`, `rm` and similar, the action worked |
| `1` | agentbox error (unknown run, bad state), or a command that crashed without a normal exit code |
| `2` | Invalid usage |
| `75` | `wait --timeout` expired; the run is still going |
| `124` | The run hit its own `--timeout` |
| `127` | The command could not be started |
| `130` | A foreground run was stopped with Ctrl+C |
| `137` | The run was stopped (`kill`, the dashboard, or a restart) |

A stopped process's raw exit code is never passed through. On Windows it is an NTSTATUS value such as `0xC000013A`, which shells read as an interrupt and abort on.

## Environment variables

| Variable | Effect |
| --- | --- |
| `AGENTBOX_HOME` | Where runs are stored (see [how it works](how-it-works.md#state-on-disk)) |
| `AGENTBOX_SHELL` | The shell used for single-argument commands |
| `AGENTBOX_AGENT` | The agent name recorded on runs started from this environment |
| `AGENTBOX_NO_SHORTCUT` | Set to `1` to have the install scripts skip `agentbox shortcut` |
| `CLAUDE_CONFIG_DIR` | Where to find Claude Code's data (default `~/.claude`) |
| `AGENTBOX_RUN_ID` | Set by agentbox inside every run, to that run's id |
