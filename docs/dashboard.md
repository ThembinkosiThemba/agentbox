# The dashboard

Run `agentbox` (or `agentbox tui`) to open it. It refreshes every second and needs a terminal of at least 40×10. From 100 columns wide, the list and the details sit side by side; on narrower terminals they stack.

The header shows the two tabs and, on the right:

- how many runs are in progress
- how many Claude background tasks are running
- how many Claude Code processes are live, and how many sessions are active

## Runs tab

Every command started through `agentbox run`, with running ones first. Each row shows status, name or command, duration, id, who launched it, and exit code if it failed.

The details pane for the selected run shows:

- status and duration
- the full command
- working directory and start/end times
- who launched it (agent and session)
- process id and shell
- timeout, and the run it restarted, if any
- a live tail of the output

Press `Enter` to open the output full screen.

| Status | Meaning |
| --- | --- |
| ⠋ running | The command is running |
| ✓ done | Exited with code 0 |
| ✗ failed | Exited with a non-zero code, or could not start |
| ■ killed | Stopped with `x` or `agentbox kill` |
| ◷ timeout | Hit its `--timeout` |
| ? lost | Its supervisor stopped responding (see [how it works](how-it-works.md#lost-runs)) |

## Claude tab

Claude Code sessions from the last 7 days, read from `~/.claude/projects` (or `$CLAUDE_CONFIG_DIR`). A session is **active** (●) if its transcript changed in the last 2 minutes. Active sessions are listed first.

The summary pane shows:

- project, branch, model and client
- prompts, turns, tokens and cost
- the most-used tools
- background tasks
- the most recent commands
- the command to resume the session

Press `Enter` on a session to open it. The list then holds the session's background tasks first, then every shell command it ran (Bash or PowerShell), newest first. Leading `cd <project> &&` prefixes are hidden in the list to keep it readable.

- **Command** details: result (OK, ERROR, INTERRUPTED, RUNNING), duration, description, the full command, and its output as Claude received it.
- **Background task** details: status, exit code, the command, Claude's completion summary, and a live tail of the task's output file. Press `Enter` again to view the output full screen.

A task shows **unknown** when Claude never recorded a completion notice, usually because the session ended first.

This tab only reads Claude Code's files. It never changes them.

## Keys

| Key | Action |
| --- | --- |
| `←` `→` · `Tab` · `1` `2` · click a tab | Switch between Runs and Claude |
| `↑` `↓` · `j` `k` · mouse wheel | Move the selection |
| `PgUp` `PgDn` · `g` `G` | Page · first · last |
| click a row | Select it; click again to open |
| `Enter` | Open: full-screen log, session details, task output |
| `Esc` · `←` | Back, or clear the filter |
| `/` | Filter the current list (matches ids, names, commands, paths, titles) |
| `?` | Show all keys |
| `q` · `Ctrl+C` | Quit |

**Runs tab**

| Key | Action |
| --- | --- |
| `n` | Start a command in the dashboard's working directory (detached, through the shell) |
| `x` | Stop the run and its whole process tree (asks first) |
| `r` | Restart: run the same command again; a running run is stopped first |
| `d` | Delete a finished run and its log (asks first) |

**Full-screen log**

| Key | Action |
| --- | --- |
| `↑` `↓` · `PgUp` `PgDn` · wheel | Scroll |
| `g` · `G` | Top · end |
| `f` | Toggle following new output |
| `w` | Toggle line wrapping |
| `x` · `r` | Stop · restart (agentbox runs only) |

Output is shown as plain text. Colors and other escape codes are removed, and progress bars that redraw with `\r` show only their latest state.
