package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"agentbox/internal/agy"
	"agentbox/internal/claude"
	"agentbox/internal/store"
)

func fixture(t *testing.T) *model {
	t.Setenv("AGENTBOX_HOME", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	start := now.Add(-3 * time.Minute)
	code := 2
	m := newModel(st, Hooks{Version: "test"})
	m.loaded = true
	m.runs = []*store.Run{
		{ID: "k3x9p2", Name: "dev", Title: "Vite dev server for the web app", Command: []string{"npm run dev -- --port 5173"}, UseShell: true, Cwd: `C:\code\app`,
			Status: store.StatusRunning, CreatedAt: start, StartedAt: &start, PID: 4242, Agent: "claude-code", SessionID: "c39bfc8a-8239"},
		{ID: "a8b2c1", Command: []string{"go", "test", "./..."}, Cwd: "/home/me/app", Status: store.StatusFailed,
			CreatedAt: start, StartedAt: &start, EndedAt: &now, ExitCode: &code},
	}
	m.sessions = []*claude.Session{{
		Agent: claude.Agent, ID: "c39bfc8a-8239-4214-bd49-20e8b5205531", Title: "Agentbox TUI", Cwd: `C:\code\agentbox`, Branch: "main",
		Model: "claude-opus-5-5", StartedAt: start, LastActivity: now, ModTime: now, Prompts: 3, Turns: 40,
		ToolCounts: map[string]int{"Bash": 24, "Read": 9, "Edit": 5},
		Execs: []*claude.Exec{
			{ToolUseID: "t1", Tool: "Bash", Command: "go build ./...", Description: "Build", Start: start, End: now, Output: "ok"},
			{ToolUseID: "t3", Tool: "Bash", Command: `cd /c/code && agentbox run -d --name dev --title "Vite dev server for the web app" -- "npm run dev"`,
				Start: start, End: start.Add(time.Second), Output: "k3x9p2 started (pid 4242): Vite dev server"},
			{ToolUseID: "t4", Tool: "Bash", Command: "agentbox wait dev --timeout 8m", Start: start.Add(time.Minute), End: now},
			{ToolUseID: "t5", Tool: "Bash", Command: "go vet ./...", Description: "Vet", Start: start.Add(2 * time.Minute), End: now,
				IsError: true, Output: "Exit code 1\n\n# agentbox/internal/tui\nview.go:6:2: imported and not used"},
			{ToolUseID: "t2", Tool: "Bash", Command: "go test ./...\nsecond line", Start: now},
		},
		Tasks: []*claude.Task{{ID: "b39ed3n6g", Command: "npm run build", Description: "Build site", Start: start, End: now,
			Status: "completed", Summary: `Background command "Build site" completed (exit code 0)`, ExitCode: new(int)}},
	}, {
		Agent: agy.Agent, ID: "41fec8a8-aee7-4a42-be74-a23460fd7234", Title: "Fix React Effect Warnings", Cwd: `C:\code\web`,
		Model: "Gemini 3.8 Flash (Low)", Entrypoint: "agy", Resume: "agy --conversation 41fec8a8-aee7-4a42-be74-a23460fd7234",
		StartedAt: start, LastActivity: now, ModTime: now, Prompts: 2, Turns: 8,
		ToolCounts: map[string]int{"run_command": 2, "view_file": 6},
		Execs: []*claude.Exec{
			{ToolUseID: "step-2", Tool: "run_command", Command: "pnpm lint", Description: "Lint", Start: start, End: now, TaskID: "task-2", Background: true},
			{ToolUseID: "step-4", Tool: "run_command", Command: "pnpm build", Description: "Build", Start: start, End: now,
				IsError: true, Output: "Exit code 1\n\nbuild failed"},
		},
		Tasks: []*claude.Task{{ID: "task-2", Command: "pnpm lint", Description: "Lint", Start: start, End: now, Status: "completed", ExitCode: new(int)}},
	}}
	m.reselect()
	return m
}

// Every rendered line must fit the terminal exactly; overflow wraps and
// wrecks the layout.
func TestViewFitsTerminal(t *testing.T) {
	sizes := [][2]int{{40, 10}, {80, 24}, {99, 30}, {100, 30}, {160, 45}, {220, 60}}
	for _, sz := range sizes {
		screens := map[string]func(m *model){
			"runs":    func(m *model) {},
			"claude":  func(m *model) { m.switchTab(tabClaude) },
			"session": func(m *model) { m.switchTab(tabClaude); m.open() },
			"task":    func(m *model) { m.switchTab(tabClaude); m.open(); m.open() },
			"command": func(m *model) { m.switchTab(tabClaude); m.open(); m.move(1) },
			"abcall":  func(m *model) { m.switchTab(tabClaude); m.open(); m.move(4) },
			"agy":     func(m *model) { m.switchTab(tabAgy) },
			"agyses":  func(m *model) { m.switchTab(tabAgy); m.open() },
			"agycmd":  func(m *model) { m.switchTab(tabAgy); m.open(); m.move(1) },
			"noagy":   func(m *model) { m.sessions = m.sessions[:1]; m.switchTab(tabAgy) },
			"log":     func(m *model) { m.open() },
			"help":    func(m *model) { m.overlay = ovHelp },
			"confirm": func(m *model) { m.ask("Stop k3x9p2 (dev server) and everything it started?", nil) },
			"input":   func(m *model) { m.openInput("new", "command", "") },
		}
		for name, setup := range screens {
			m := fixture(t)
			m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
			setup(m)
			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) != sz[1] {
				t.Errorf("%dx%d %s: %d lines, want %d", sz[0], sz[1], name, len(lines), sz[1])
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w > sz[0] {
					t.Errorf("%dx%d %s: line %d is %d wide: %q", sz[0], sz[1], name, i, w, ansi.Strip(l))
				}
			}
			if os.Getenv("AGENTBOX_DUMP") != "" && sz[0] == 160 && (name == "runs" || name == "claude" || name == "session" || name == "abcall") {
				t.Logf("\n%s", ansi.Strip(out))
			}
		}
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"\x1b[31mred\x1b[0m": "red",
		"10%\r50%\r100%":     "100%",
		"done\r":             "done",
		"a\tb":               "a   b",
		"bell\x07":           "bell",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
