# Using agentbox with coding agents

Coding agents run shell commands through a tool with a time limit. In Claude Code, a command is stopped after 10 minutes in the foreground, or 30 minutes in the background. Long builds, test suites and dev servers get killed partway through, and you can't see them while they run.

If an agent starts these commands with `agentbox run -d` instead, they run outside the agent's shell tool:

- they keep going after the tool times out, or after the agent's process tree is killed
- they appear in the agentbox dashboard, where you can watch, stop and restart them
- the agent can check on them, wait for them and read their output at any time

## Telling your agent about agentbox

`agentbox guide` prints a short set of instructions for agents. Append it to the instructions file your agent reads:

```sh
agentbox guide >> ~/.claude/CLAUDE.md   # every Claude Code session on this machine
agentbox guide >> CLAUDE.md             # one project, Claude Code
agentbox guide >> AGENTS.md             # one project, Codex and other agents
```

## The pattern agents follow

```sh
agentbox run -d --name build -- "npm run build"   # start detached; returns at once with the run id
agentbox wait build --timeout 8m                   # block until done; exits with the command's code
agentbox logs build -n 50                          # last 50 lines of output
agentbox status build                              # status, exit code, duration
agentbox kill build                                # stop it and everything it started
```

- **Keep `wait --timeout` below the shell tool's limit.** If the timeout expires first, `wait` exits `75` and the run keeps going. The agent should call `wait` again, not start the command over.
- **Dev servers** should be started with `run -d` and left running. You can see them in the dashboard and stop them there.
- **Cap a runaway command** with `run --timeout 30m`. When the run hits it, the run stops and reports exit code `124`.
- **Name runs** so they're easy to refer to later. When several runs share a name, it refers to the newest one.

All exit codes are listed in the [CLI reference](cli.md#exit-codes).

## Attribution

Every run records who launched it. The dashboard and `agentbox ls` show this in the **by** column:

- **Claude Code** is detected automatically from `CLAUDE_CODE_SESSION_ID`. The run is linked to the exact session that started it.
- **Other agents** can set `AGENTBOX_AGENT=<name>` in their environment.
- Runs you start yourself show as **you**.

The command also gets `AGENTBOX_RUN_ID` in its environment, in case it needs to know its own run.

## What agentbox sees without the wrapper

The **Claude** tab reads Claude Code's own transcripts, so it shows every session's commands and `run_in_background` tasks even when the agent never calls agentbox. That view is read-only. agentbox can show Claude's own background tasks and their output, but can't stop them, because Claude doesn't record their process ids. Commands that need to be stoppable should go through `agentbox run`.
