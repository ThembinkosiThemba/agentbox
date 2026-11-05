// Package guide holds the instructions agentbox gives coding agents and
// writes them into the instruction files those agents read.
package guide

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Markers delimit the block agentbox owns, so installing again replaces it
// instead of appending a second copy.
const (
	Begin = "<!-- agentbox:begin -->"
	End   = "<!-- agentbox:end -->"
)

// Change says what Install did to a file.
type Change int

const (
	Unchanged Change = iota
	Added
	Updated
)

func (c Change) String() string {
	switch c {
	case Added:
		return "added"
	case Updated:
		return "updated"
	default:
		return "unchanged"
	}
}

// Target is one agent and the files it reads.
type Target struct {
	Key     string // name used with --agent
	Name    string
	Global  string // user-wide instruction file
	Project string // instruction file in the project root
}

// Targets lists every agent agentbox knows how to install into.
func Targets() []Target {
	home, _ := os.UserHomeDir()
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	return []Target{
		{Key: "claude", Name: "Claude Code", Global: filepath.Join(home, ".claude", "CLAUDE.md"), Project: "CLAUDE.md"},
		{Key: "antigravity", Name: "Antigravity CLI", Global: filepath.Join(home, ".gemini", "GEMINI.md"), Project: "GEMINI.md"},
		{Key: "opencode", Name: "OpenCode", Global: filepath.Join(config, "opencode", "AGENTS.md"), Project: "AGENTS.md"},
		{Key: "codex", Name: "Codex and other AGENTS.md agents", Global: filepath.Join(home, ".codex", "AGENTS.md"), Project: "AGENTS.md"},
	}
}

// Detected returns the targets whose user-wide config directory already
// exists, so --install without --agent only touches agents in use.
func Detected() []Target {
	var out []Target
	for _, t := range Targets() {
		if fi, err := os.Stat(filepath.Dir(t.Global)); err == nil && fi.IsDir() {
			out = append(out, t)
		}
	}
	return out
}

// Select resolves --agent names. Keys and common aliases are accepted.
func Select(names []string) ([]Target, error) {
	byKey := make(map[string]Target, len(Targets()))
	for _, t := range Targets() {
		byKey[t.Key] = t
	}
	aliases := map[string]string{
		"claude-code": "claude",
		"agy":         "antigravity",
		"gemini":      "antigravity",
		"gemini-cli":  "antigravity",
	}
	var out []Target
	seen := make(map[string]bool)
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if a, ok := aliases[key]; ok {
			key = a
		}
		t, ok := byKey[key]
		if !ok {
			return nil, fmt.Errorf("unknown agent %q (try claude, antigravity, opencode or codex)", name)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, errors.New("no agents given")
	}
	return out, nil
}

// Install adds content to path inside agentbox markers, replacing an earlier
// block in place. It creates the file and its parent directories if needed.
func Install(path, content string) (Change, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeFile(path, []byte(block("\n", content))); err != nil {
			return Unchanged, err
		}
		return Added, nil
	}
	if err != nil {
		return Unchanged, err
	}

	s := string(data)
	eol := lineEnding(s)
	next := block(eol, content)

	start := strings.Index(s, Begin)
	if start >= 0 {
		end := blockEnd(s, start)
		if end < 0 {
			return Unchanged, fmt.Errorf("%s has %s without %s", path, Begin, End)
		}
		out := s[:start] + next + s[end:]
		if out == s {
			return Unchanged, nil
		}
		if err := writeFile(path, []byte(out)); err != nil {
			return Unchanged, err
		}
		return Updated, nil
	}

	if s != "" && !strings.HasSuffix(s, "\n") {
		s += eol
	}
	if s != "" && !strings.HasSuffix(s, eol+eol) {
		s += eol
	}
	if err := writeFile(path, []byte(s+next)); err != nil {
		return Unchanged, err
	}
	return Added, nil
}

// Remove takes agentbox's block out of path. It reports whether the file
// changed; a missing file or one without a block is not an error.
func Remove(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	s := string(data)
	start := strings.Index(s, Begin)
	if start < 0 {
		return false, nil
	}
	end := blockEnd(s, start)
	if end < 0 {
		return false, fmt.Errorf("%s has %s without %s", path, Begin, End)
	}

	left := s[:start]
	if strings.HasSuffix(left, "\r\n\r\n") {
		left = left[:len(left)-2]
	} else if strings.HasSuffix(left, "\n\n") {
		left = left[:len(left)-1]
	}
	if err := writeFile(path, []byte(left+s[end:])); err != nil {
		return false, err
	}
	return true, nil
}

// blockEnd returns the offset just past End, including its line ending.
func blockEnd(s string, start int) int {
	rel := strings.Index(s[start:], End)
	if rel < 0 {
		return -1
	}
	end := start + rel + len(End)
	if strings.HasPrefix(s[end:], "\r\n") {
		return end + 2
	}
	if strings.HasPrefix(s[end:], "\n") {
		return end + 1
	}
	return end
}

func block(eol, content string) string {
	return Begin + eol + strings.TrimRight(content, "\r\n") + eol + End + eol
}

func lineEnding(s string) string {
	if strings.Contains(s, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Content is the guide printed by `agentbox guide` and installed into agent
// instruction files.
const Content = `## Long-running commands: use agentbox

This machine has ` + "`agentbox`" + `, which runs commands outside your shell tool's time limit
and lets the user watch and manage them in a dashboard. Use it for any command that may
take more than about a minute (builds, test suites, installs, migrations, dev servers)
instead of running it directly or in the background.

` + "```" + `sh
agentbox run -d --name build --title "Build the web app" -- "npm run build"
                                     # start detached; prints the run id
agentbox wait build --timeout 8m     # block until done; exits with the command's code
agentbox logs build -n 50            # last 50 lines of output (-f to follow)
agentbox status build                # status, exit code, duration
agentbox kill build                  # stop it and everything it started
agentbox ls                          # recent runs (-a for all)
` + "```" + `

- A single quoted argument runs through the shell (pipes, &&, env vars work).
- ` + "`wait`" + ` exits with the command's own code. If its --timeout expires first it exits 75
  and the run keeps going: call ` + "`agentbox wait`" + ` again instead of restarting the command.
  Keep --timeout below your shell tool's limit.
- Use ` + "`--timeout 30m`" + ` on ` + "`run`" + ` to cap how long a command may run (exit 124 when hit).
  A run that was stopped exits 137.
- Start dev servers with ` + "`run -d`" + ` and leave them running; the user can see and stop them.
- Names are reusable: the newest run with a name wins.
- Give every run a short ` + "`--title`" + ` saying what it is for, in plain words. The user reads
  it in the dashboard; the name is for you to refer to the run.
`
