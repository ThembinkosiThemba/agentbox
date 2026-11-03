# The dashboard

Run `agentbox` (or `agentbox tui`) to open it. It refreshes every second and needs a terminal of at least 40×10. From 100 columns wide, the list and the details sit side by side; on narrower terminals they stack.

The header shows the two tabs and, on the right:

- how many runs are in progress
- how many background tasks agents have running
- how many Claude Code processes are live, and how many sessions are active

## Runs tab

Every command started through `agentbox run`, with running ones first. Each row shows status, the run's title (or name, or command), duration, then its id, name, project folder and command, who launched it and when. A failed run shows its exit code in red instead.

The details pane for the selected run shows:

- status and duration
- the full command
- working directory and start/end times
- who launched it, with the Claude session's title when it came from one
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
- **commands over time**: a sparkline of when the session ran commands, red where one failed and yellow where one is still running
- **tool mix**: one strip showing how the session split its tool calls, with a legend. MCP tools are grouped by server
- background tasks
- the most recent commands, with the time each started, Claude's description of it and how long it took, and a count of how many succeeded and failed
- the command to resume the session

Commands that call agentbox are marked with ▣ and read by what they do, e.g. `run · Build the web app` or `wait · Build the web app`, using the run's `--title` (or name).

Press `Enter` on a session to open it. The list then holds the session's background tasks first, then every shell command it ran (Bash or PowerShell), newest first. Leading `cd <project> &&` prefixes are hidden in the list to keep it readable.

- **Command** details: result (OK, ERROR, INTERRUPTED, RUNNING), duration, description, the full command, and its output as Claude received it. A failed command's row shows its exit code and first line of error output. For an agentbox call, the details also show the run it started or waited on, with its live status.
- **Background task** details: status, exit code, the command, Claude's completion summary, and a live tail of the task's output file. Press `Enter` again to view the output full screen.

A task shows **unknown** when Claude never recorded a completion notice, usually because the session ended first.

This tab only reads Claude Code's files. It never changes them.

## Agy tab

Antigravity CLI (`agy`) sessions from the last 7 days, read from `~/.gemini/antigravity-cli`. It works like the Claude tab, with the same summary, command list and background tasks, and it only reads agy's files.

agy records less than Claude Code, so a few things differ:

- no branch, client version or cost, and token counts only from recent versions of agy
- a command's duration runs from when it was requested to when it finished, so it includes any time agy spent waiting for you to approve it
- a command that never got a result shows as **INTERRUPTED**, because agy writes nothing when one is cut off
- agy's processes aren't counted in the header's **live** figure, because it keeps no record of them. Its sessions do count as **active**

## Keys

| Key | Action |
| --- | --- |
| `←` `→` · `Tab` · `1` `2` `3` · click a tab | Switch between Runs, Claude and Agy |
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
