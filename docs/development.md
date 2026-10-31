# Development

This requires Go 1.26 or later. agentbox is pure Go (no cgo), so it cross-compiles to every platform from any machine.

```sh
go build -o agentbox .     # agentbox.exe on Windows
go test ./...
```

Use a separate state directory while testing, so your real runs stay untouched:

```sh
AGENTBOX_HOME=/tmp/agentbox-dev ./agentbox run -d -- "sleep 5"
AGENTBOX_HOME=/tmp/agentbox-dev ./agentbox
```

The platform code is split between `proc_windows.go` and `proc_unix.go`, so check that both compile whichever OS you work on:

```sh
GOOS=linux go vet ./...
GOOS=windows go vet ./...
GOOS=darwin go vet ./...
```

## Layout

| Path | Contents |
| --- | --- |
| `main.go` | Entry point; hands off to `internal/cli` |
| `internal/cli` | Command parsing and output |
| `internal/store` | Run records on disk: `meta.json`, logs, heartbeats, kill markers |
| `internal/supervisor` | The run lifecycle: create, spawn detached, supervise, kill |
| `internal/proc` | Platform process handling: Job Objects on Windows, process groups elsewhere |
| `internal/claude` | Read-only, incremental parser for Claude Code transcripts |
| `internal/tui` | The dashboard (Bubble Tea and Lip Gloss) |
| `internal/human` | Formatting of durations, times and sizes |

The rules contributors need to keep are in [AGENTS.md](../AGENTS.md).

## Tests

- `internal/supervisor` runs real child processes, using the test binary itself as the child. It covers exit codes, output capture, kill and timeout on every platform.
- `internal/tui` renders every screen at several terminal sizes and fails if any line is wider than the terminal. It also covers keyboard and mouse navigation.

CI ([ci.yml](../.github/workflows/ci.yml)) runs the tests on Linux, Windows and macOS for every push to `main` and every pull request. On Linux it also runs them with the race detector.

## Releasing

Push a version tag:

```sh
git tag v0.2.0
git push origin v0.2.0
```

The [release workflow](../.github/workflows/release.yml) then:

1. runs the full test suite on Linux, Windows and macOS
2. builds static binaries for Linux, macOS and Windows on x64 and ARM, with the version from the tag
3. checks that the Linux binary runs
4. publishes the binaries and `checksums.txt` to a GitHub release with generated notes
