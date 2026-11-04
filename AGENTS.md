# agentbox: notes for agents working on this repo

agentbox is a Go TUI + CLI that shows and manages commands run by coding agents. See README.md and docs/ for what it does from a user's point of view.

## Layout

```
main.go                     entry; hands off to internal/cli
internal/cli                command parsing and output (flags anywhere: `logs abc -f`)
internal/guide              `agentbox guide`: the agent instructions and installing them into agent files
internal/store              on-disk run records: meta.json, output.log, heartbeat, kill marker
internal/supervisor         Create / SpawnDetached / Supervise / Kill: the run lifecycle
internal/proc               platform code: proc_windows.go (Job Objects), proc_unix.go (process groups)
internal/claude             read-only, incremental parser for ~/.claude/projects/*/*.jsonl; also the Session/Exec/Task types every agent's sessions use
internal/agy                the same for Antigravity CLI: ~/.gemini/antigravity-cli/brain/*/.system_generated/logs/transcript_full.jsonl
internal/opencode           read-only, incremental reader for OpenCode's SQLite DB: ~/.local/share/opencode/opencode.db
internal/tui                Bubble Tea dashboard: tui.go (model/update), view.go, layout.go, styles.go
internal/human              duration/time/size formatting
internal/launcher           `agentbox shortcut`: Start menu .lnk (via PowerShell), Linux .desktop, macOS .command
tools/genicon               draws the icon in code -> assets/icon/, internal/launcher/icon.png
winres/, rsrc_windows_*.syso    icon + version resources linked into agentbox.exe (regenerate with go-winres)
```

## Invariants

- **No daemon.** State lives on disk. A run's supervisor is the only writer of its `meta.json`. Other processes ask it to stop by writing the `kill` marker. They write `meta.json` themselves only when the supervisor is gone (status `lost`).
- `lost` is derived (a stale heartbeat), never stored.
- `internal/claude` and `internal/agy` must never write to the agent's files. Transcripts are append-only, and the scanners cache byte offsets. Keep parsing incremental.
- agy's results carry no call id and its steps can be written out of order: the result of a turn's nth tool call (from 0) is step `turn + 1 + n`, which may come before the turn itself. A command with no result was interrupted.
- Exit codes are part of the CLI contract (see docs/cli.md). Never pass through a stopped process's raw code: on Windows it is an NTSTATUS value that shells treat as an interrupt.
- Don't open log files with `O_APPEND` for children. On Windows that creates an append-only handle that Git Bash children can't write through.
- Every TUI line must fit the terminal exactly. `TestViewFitsTerminal` checks this across sizes and screens. Use `fit`/`trunc` from layout.go, never raw `len` on styled strings.
- A run's output carries the command's own colours. `sanitize` keeps SGR sequences, drops every other escape sequence and seals the line; only use `plain` when embedding output in a line that has a colour of its own. Children are started with colour forced on, so strip it at the CLI edge when stdout is not a terminal.

## Working on it

```sh
go build -o agentbox.exe .        # or `go build .` on Linux
go test ./...
GOOS=linux go vet ./...           # also on Windows: the platform files must both compile
AGENTBOX_HOME=/tmp/abtest ./agentbox run -d -- "sleep 5"   # keep test runs out of your real state
```

Style: standard gofmt, small packages, comments only where the reason isn't obvious. Bubble Tea v1, Lip Gloss v1, `charmbracelet/x/ansi` for width-aware string handling.

## Releases

`.github/workflows/release.yml` runs on `v*` tags: it reuses `ci.yml` (tests on Linux, Windows and macOS), then builds `CGO_ENABLED=0` binaries for linux, darwin and windows on amd64 and arm64, and publishes them with `checksums.txt`. The version comes from the tag via `-X agentbox/internal/cli.Version`. Keep the code pure Go (no cgo) so cross-compiling keeps working.

`install.sh` (Linux/macOS, POSIX sh, shellcheck-clean) and `install.ps1` (Windows PowerShell 5.1+) download a release binary, verify it against `checksums.txt` and set up PATH. Both read the same `AGENTBOX_*` variables; keep them in step. Both finish by running `agentbox shortcut` (skipped with `AGENTBOX_NO_SHORTCUT=1`, and on Linux machines without a desktop).
