package agy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/claude"
)

const sessionID = "11111111-2222-3333-4444-555555555555"

// write lays out a conversation the way agy does, and returns the scanner and
// the transcript path.
func write(t *testing.T, id string, lines ...string) (*Scanner, string) {
	t.Helper()
	root := t.TempDir()
	logs := filepath.Join(root, "brain", id, ".system_generated", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logs, "transcript_full.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Scanner{root: root, files: map[string]*fileState{}}, path
}

func user(idx int, text string) string {
	return mustJSON(map[string]any{"step_index": idx, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE",
		"created_at": "2026-10-05T14:58:27Z", "content": "<USER_REQUEST>\n" + text + "\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nnoise\n</ADDITIONAL_METADATA>"})
}

func planner(idx int, calls ...map[string]any) string {
	return mustJSON(map[string]any{"step_index": idx, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE",
		"created_at": "2026-10-05T14:58:30Z", "input_tokens": 100, "cache_read_tokens": 50, "output_tokens": 7, "tool_calls": calls})
}

func command(line, summary string) map[string]any {
	return map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": line, "Cwd": `C:\code\app`,
		"WaitMsBeforeAsync": 5000, "toolAction": "Doing it", "toolSummary": summary}}
}

func other(name string) map[string]any {
	// StartLine has been seen as both a number and a string; a tool's odd
	// arguments must not cost the rest of the step.
	return map[string]any{"name": name, "args": map[string]any{"StartLine": "1"}}
}

func exited(idx, code int, out string) string {
	return mustJSON(map[string]any{"step_index": idx, "source": "MODEL", "type": "GENERIC", "status": "DONE",
		"created_at": "2026-10-05T14:58:32Z",
		"content":    "Created At: 2026-10-05T16:58:32+02:00\nCompleted At: 2026-10-05T16:59:02+02:00\n\n\t\t\t\tThe command exited with code " + itoa(code) + ".\n\t\t\t\tOutput:\n\t\t\t\t" + out + "\r\n"})
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func scanOne(t *testing.T, sc *Scanner) *claude.Session {
	t.Helper()
	ss, err := sc.Scan(time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want 1", len(ss))
	}
	return ss[0]
}

func TestSession(t *testing.T) {
	sc, _ := write(t, sessionID,
		user(0, "fix the build"),
		mustJSON(map[string]any{"step_index": 2, "type": "USER_INPUT", "created_at": "2026-10-05T15:00:00Z",
			"content": "<USER_REQUEST>\nand the tests\n</USER_REQUEST>\n<USER_SETTINGS_CHANGE>\nThe user changed setting `Model Selection` from None to Gemini 3.8 Flash (Low). No need to comment.\n</USER_SETTINGS_CHANGE>"}),
		planner(3),
	)
	s := scanOne(t, sc)
	if s.Agent != Agent || s.ID != sessionID {
		t.Errorf("Agent, ID = %q, %q", s.Agent, s.ID)
	}
	if s.Prompts != 2 || s.FirstAsk != "fix the build" || s.LastPrompt != "and the tests" {
		t.Errorf("Prompts, FirstAsk, LastPrompt = %d, %q, %q", s.Prompts, s.FirstAsk, s.LastPrompt)
	}
	if s.Model != "Gemini 3.8 Flash (Low)" {
		t.Errorf("Model = %q", s.Model)
	}
	if s.Turns != 1 || s.InputTokens != 150 || s.OutputTokens != 7 {
		t.Errorf("Turns, InputTokens, OutputTokens = %d, %d, %d", s.Turns, s.InputTokens, s.OutputTokens)
	}
	if s.Resume != "agy --conversation "+sessionID {
		t.Errorf("Resume = %q", s.Resume)
	}
}

func TestCommandResults(t *testing.T) {
	sc, _ := write(t, sessionID,
		user(0, "go"),
		planner(1, command("go build ./...", "Build")),
		exited(2, 0, "built"),
		planner(3, command("go vet ./...", "Vet")),
		exited(4, 1, "vet: bad"),
	)
	s := scanOne(t, sc)
	if len(s.Execs) != 2 {
		t.Fatalf("got %d execs", len(s.Execs))
	}
	ok, failed := s.Execs[0], s.Execs[1]
	if ok.IsError || ok.Output != "built" || ok.Description != "Build" || ok.Tool != "run_command" {
		t.Errorf("ok = %+v", ok)
	}
	// Timed from the result's own stamps, not the step's created_at.
	if d := ok.Duration(time.Now()); d != 30*time.Second {
		t.Errorf("duration = %v", d)
	}
	if !failed.IsError || failed.Output != "Exit code 1\n\nvet: bad" {
		t.Errorf("failed = %+v", failed)
	}
	if s.ToolCounts["run_command"] != 2 {
		t.Errorf("ToolCounts = %v", s.ToolCounts)
	}
	if s.Cwd != `C:\code\app` {
		t.Errorf("Cwd = %q", s.Cwd)
	}
}

// Results carry no call id and steps can be written out of order, so pairing
// goes by step index: parallel calls must not swap, and a result written
// before its tool call must still find it.
func TestResultPairing(t *testing.T) {
	sc, _ := write(t, sessionID,
		user(0, "go"),
		exited(3, 3, "second"), // the planner step below hasn't been written yet
		planner(1, command("one", "One"), command("two", "Two"), other("view_file")),
		mustJSON(map[string]any{"step_index": 4, "type": "GENERIC", "status": "DONE", "created_at": "2026-10-05T14:58:32Z", "content": "File Path: x"}),
		exited(2, 0, "first"),
	)
	s := scanOne(t, sc)
	if len(s.Execs) != 2 {
		t.Fatalf("got %d execs", len(s.Execs))
	}
	if e := s.Execs[0]; e.Command != "one" || e.Output != "first" || e.IsError {
		t.Errorf("first = %+v", e)
	}
	if e := s.Execs[1]; e.Command != "two" || e.Output != "Exit code 3\n\nsecond" || !e.IsError {
		t.Errorf("second = %+v", e)
	}
	if s.ToolCounts["view_file"] != 1 {
		t.Errorf("ToolCounts = %v", s.ToolCounts)
	}
}

func TestInterrupted(t *testing.T) {
	sc, path := write(t, sessionID,
		user(0, "go"),
		planner(1, command("sleep 100", "Wait")),
		user(3, "stop that"), // step 2 was never written
	)
	s := scanOne(t, sc)
	if e := s.Execs[0]; !e.Interrupted || !e.Done() {
		t.Errorf("exec = %+v", e)
	}

	// A result that does turn up later wins.
	appendTo(t, path, exited(2, 0, "finished after all"))
	s = scanOne(t, sc)
	if e := s.Execs[0]; e.Interrupted || e.Output != "finished after all" {
		t.Errorf("exec = %+v", e)
	}
}

func TestPendingCommand(t *testing.T) {
	sc, path := write(t, sessionID, user(0, "go"), planner(1, command("npm test", "Test")))
	s := scanOne(t, sc)
	if e := s.Execs[0]; e.Done() {
		t.Errorf("live session: exec = %+v, want still running", e)
	}

	// Once the session goes idle nothing more will come for it.
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	s = scanOne(t, sc)
	if e := s.Execs[0]; !e.Done() || !e.Interrupted {
		t.Errorf("idle session: exec = %+v, want interrupted", e)
	}
}

func TestPermissionDenied(t *testing.T) {
	sc, _ := write(t, sessionID,
		user(0, "go"),
		planner(1, command("rm -rf x", "Remove")),
		mustJSON(map[string]any{"step_index": 2, "type": "GENERIC", "status": "ERROR", "created_at": "2026-10-05T14:58:32Z",
			"error":   "permission check failed for command",
			"content": "Created At: 2026-10-05T16:58:32+02:00\nCompleted At: 2026-10-05T16:59:02+02:00\nEncountered error in step execution: permission check failed for command"}),
	)
	if e := scanOne(t, sc).Execs[0]; !e.IsError || e.Output != "permission check failed for command" {
		t.Errorf("exec = %+v", e)
	}
}

func background(idx int, logPath string) string {
	return mustJSON(map[string]any{"step_index": idx, "type": "RUN_COMMAND", "status": "RUNNING", "created_at": "2026-10-05T14:58:32Z",
		"content": "Created At: 2026-10-05T16:58:32+02:00\nTool is running as a background task with task id: " + sessionID + "/task-" + itoa(idx) +
			"\nTask Description: npm run dev\nTask logs are available at: " + logPath})
}

func TestBackgroundTask(t *testing.T) {
	sc, path := write(t, sessionID,
		user(0, "go"),
		planner(1, command("npm run dev", "Dev server")),
		background(2, "file:///C:/Users/me/.gemini/brain/task%202.log"),
	)
	s := scanOne(t, sc)
	if len(s.Tasks) != 1 {
		t.Fatalf("got %d tasks", len(s.Tasks))
	}
	task := s.Tasks[0]
	if task.ID != "task-2" || task.Command != "npm run dev" || task.Description != "Dev server" {
		t.Errorf("task = %+v", task)
	}
	if want := filepath.FromSlash("C:/Users/me/.gemini/brain/task 2.log"); task.OutputFile != want {
		t.Errorf("OutputFile = %q, want %q", task.OutputFile, want)
	}
	if e := s.Execs[0]; e.TaskID != "task-2" || !e.Background || e.IsError {
		t.Errorf("exec = %+v", e)
	}
	if got := task.State(s, time.Now()); got != claude.TaskRunning {
		t.Errorf("state = %q", got)
	}

	appendTo(t, path, mustJSON(map[string]any{"step_index": 3, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE", "created_at": "2026-10-05T15:30:00Z",
		"content": "The following is a <SYSTEM_MESSAGE>\n[Message] timestamp=2026-10-05T15:20:00Z sender=" + sessionID + "/task-2 priority=MESSAGE_PRIORITY_HIGH content=Task id \"" + sessionID + "/task-2\" finished with result:\n\n\t\t\t\tThe command exited with code 2.\n\t\t\t\tOutput:\n\t\t\t\tboom\n</SYSTEM_MESSAGE>"}))
	s = scanOne(t, sc)
	task = s.Tasks[0]
	if task.ExitCode == nil || *task.ExitCode != 2 || task.State(s, time.Now()) != claude.TaskFailed {
		t.Errorf("finished task = %+v", task)
	}
	if want := time.Date(2026, 10, 5, 15, 20, 0, 0, time.UTC); !task.End.Equal(want) {
		t.Errorf("End = %v, want %v", task.End, want)
	}
}

func TestTasksStoppedByRestart(t *testing.T) {
	sc, _ := write(t, sessionID,
		user(0, "go"),
		planner(1, command("npm run dev", "Dev server")),
		background(2, "file:///tmp/task-2.log"),
		mustJSON(map[string]any{"step_index": 3, "type": "SYSTEM_MESSAGE", "created_at": "2026-10-05T15:30:00Z",
			"content": "[Message] timestamp=2026-10-05T15:30:00Z sender=system priority=MESSAGE_PRIORITY_LOW content=[Notice] All your subagents and background tasks have been stopped due to server restart."}),
	)
	s := scanOne(t, sc)
	if got := s.Tasks[0].State(s, time.Now()); got != claude.TaskKilled {
		t.Errorf("state = %q", got)
	}
}

func appendTo(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// A line still being written must be picked up whole on a later refresh.
func TestIncremental(t *testing.T) {
	sc, path := write(t, sessionID, user(0, "go"))
	if s := scanOne(t, sc); s.Turns != 0 {
		t.Fatalf("Turns = %d", s.Turns)
	}
	line := planner(1, command("ls", "List"))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(line[:20])
	if s := scanOne(t, sc); s.Turns != 0 {
		t.Fatalf("after partial line: Turns = %d", s.Turns)
	}
	f.WriteString(line[20:] + "\n")
	s := scanOne(t, sc)
	if s.Turns != 1 || len(s.Execs) != 1 || s.Prompts != 1 {
		t.Fatalf("after full line: Turns = %d, Execs = %d, Prompts = %d", s.Turns, len(s.Execs), s.Prompts)
	}
}

// Snapshots handed out must not change as the scanner reads further.
func TestSnapshotIsolated(t *testing.T) {
	sc, path := write(t, sessionID, user(0, "go"), planner(1, command("ls", "List")))
	before := scanOne(t, sc)
	appendTo(t, path, exited(2, 0, "done"))
	scanOne(t, sc)
	if e := before.Execs[0]; e.Done() {
		t.Errorf("earlier snapshot changed: %+v", e)
	}
}

func TestMetadata(t *testing.T) {
	sc, _ := write(t, sessionID, user(0, "a long first question"), planner(1, command("ls", "List")))
	s := scanOne(t, sc)
	if s.DisplayTitle() != "a long first question" {
		t.Errorf("no metadata: title = %q", s.DisplayTitle())
	}

	cache := filepath.Join(sc.root, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	put := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(cache, "last_conversations.json"), mustJSON(map[string]string{`C:\from\last`: sessionID}))
	if s = scanOne(t, sc); s.Cwd != `C:\from\last` {
		t.Errorf("Cwd = %q", s.Cwd)
	}

	put(filepath.Join(cache, "conversation_metadata.json"), mustJSON(map[string]any{"conversations": map[string]any{
		sessionID: map[string]any{"summary": map[string]any{"Title": "", "Preview": "From preview", "WorkspaceURIs": []string{"file:///C:/from/meta%20dir"}}},
	}}))
	s = scanOne(t, sc)
	if s.Title != "From preview" {
		t.Errorf("Title = %q", s.Title)
	}
	if want := filepath.FromSlash("C:/from/meta dir"); s.Cwd != want {
		t.Errorf("Cwd = %q, want %q", s.Cwd, want)
	}

	annotations := filepath.Join(sc.root, "annotations")
	if err := os.MkdirAll(annotations, 0o755); err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(annotations, sessionID+".pbtxt"), "title:\"Generated \\\"title\\\"\"\n")
	if s = scanOne(t, sc); s.Title != `Generated "title"` {
		t.Errorf("Title = %q", s.Title)
	}
}

func TestInternalConversationsHidden(t *testing.T) {
	sc, _ := write(t, sessionID, user(0, "go"))
	cache := filepath.Join(sc.root, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	data := mustJSON(map[string]any{"conversations": map[string]any{sessionID: map[string]any{"is_internal": true}}})
	if err := os.WriteFile(filepath.Join(cache, "conversation_metadata.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := sc.Scan(time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Errorf("got %d sessions, want none", len(ss))
	}
}

func TestMissingDataDir(t *testing.T) {
	sc := &Scanner{root: filepath.Join(t.TempDir(), "nope"), files: map[string]*fileState{}}
	ss, err := sc.Scan(time.Hour, 0)
	if err != nil || len(ss) != 0 {
		t.Errorf("Scan = %v, %v", ss, err)
	}
}
