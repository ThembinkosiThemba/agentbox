# How it works

agentbox has no daemon and no server. Everything is plain files on disk. Every agentbox process (the CLI, any number of dashboards, the supervisors) works by reading and writing those files.

## State on disk

| OS | Location |
| --- | --- |
| Windows | `%LOCALAPPDATA%\agentbox\runs\<id>\` |
| Linux, macOS | `$XDG_STATE_HOME/agentbox/runs/<id>/`, default `~/.local/state/agentbox/runs/<id>/` |

Set `AGENTBOX_HOME` to put state somewhere else. Each run directory contains:

| File | Contents |
| --- | --- |
| `meta.json` | The run record: command, directory, status, times, exit code, pids, who launched it |
| `output.log` | Combined stdout and stderr |
| `heartbeat` | Touched by the supervisor every 2 seconds |
| `kill` | Created to ask the supervisor to stop the run |

`meta.json` is written atomically (write to a temporary file, then rename), so readers never see a half-written file.

## Supervisors

Every run is owned by a **supervisor**: an agentbox process that starts the command, writes its output to the log, sends heartbeats, enforces the timeout, and records the outcome. The supervisor is the only process that writes the run's `meta.json`.

- **`agentbox run`** (foreground): the agentbox process you started is the supervisor. Output goes to both your terminal and the log.
- **`agentbox run -d`**: agentbox starts a separate supervisor process (`agentbox __supervise <id>`), waits until it reports that the command has started, then returns. The supervisor is detached from the caller:
  - **Windows:** started with no console window, in its own process group, and outside the caller's job object where the job allows it.
  - **Linux and macOS:** started in a new session.

  Because it is detached, the caller can exit, time out, or have its whole process tree killed, and the run keeps going. This is what lets a run outlive the agent's shell tool.

## Process trees

The supervisor starts the command in a group that contains it and everything it spawns:

- **Windows:** a named Job Object (`Local\agentbox-<id>`). Closing the job kills every process in it, and the job's name lets another agentbox process find and kill it.
- **Linux and macOS:** a new process group, led by the command.

When a run is stopped:

1. `agentbox kill` (or the dashboard) creates the run's `kill` file.
2. The supervisor sees it within a quarter of a second and sends the whole tree Ctrl+Break (Windows) or SIGTERM (Linux, macOS).
3. Anything still running 5 seconds later is force-killed.
4. The supervisor records the run as **killed**.

A timeout follows the same steps and records the run as **timeout**.

When the command exits on its own, anything it left running in its tree (a forgotten `&` background process, for example) is cleaned up too.

## Lost runs

If a run is marked running but its heartbeat is more than 15 seconds old, its supervisor is gone, for example after a crash or a reboot. agentbox then shows the run as **lost**. This status is worked out each time the run is read and is never saved to disk.

On Windows the command dies with its supervisor, because the job closes with it. Stopping a lost run kills whatever is left of its tree directly and marks it killed.

## Claude Code sessions

The Claude tab and `agentbox sessions` read the transcripts Claude Code keeps at `~/.claude/projects/<project>/<session>.jsonl` (or under `$CLAUDE_CONFIG_DIR`). From each transcript agentbox takes:

- the session's title, directory, branch, model, client version, token usage and cost
- every Bash and PowerShell tool call, matched to its result for duration, outcome and output
- every background task: the call that started it, its output file, and Claude's completion notice with the exit code

Live Claude Code processes are counted from the per-process files in `~/.claude/sessions/`.

Transcripts only grow, so agentbox remembers how far into each file it has read and parses only new lines on each refresh. That keeps refreshes fast even for sessions that are several megabytes long.

agentbox never writes to any of Claude Code's files.

## Antigravity CLI sessions

The Agy tab and `agentbox sessions` read the transcripts `agy` keeps at `~/.gemini/antigravity-cli/brain/<conversation>/.system_generated/logs/transcript_full.jsonl`, incrementally in the same way. From each transcript agentbox takes prompts, model, token usage, every `run_command` call matched to its result, and every background task with its log file and completion notice.

Titles come from `annotations/<conversation>.pbtxt` and `cache/conversation_metadata.json`, and the project directory from that cache or `cache/last_conversations.json`. These files cover only some conversations, so agentbox falls back to the first prompt and the directory of the first command.

agentbox never writes to any of agy's files.

## OpenCode sessions

The OpenCode tab and `agentbox sessions` read OpenCode's SQLite database, `~/.local/share/opencode/opencode.db` (or under `$XDG_DATA_HOME`, or wherever `OPENCODE_DB` points). `session_v2` rows give each session's title, directory, model, client version, token usage and cost; `session_message` rows give its prompts, turns, tool calls and background shells.

Unlike a transcript, the database is updated in place: a message is rewritten as its tool calls start and finish. agentbox keeps what it parsed for each session and re-reads only messages whose `time_updated` changed, then rebuilds that session's totals from them, so a rewritten message is never counted twice.

Agentbox opens the database read-only and never writes to it. Child sessions (subagents) and archived sessions are skipped.
