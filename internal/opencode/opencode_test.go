package opencode

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/claude"
)

const schema = `
create table session_v2 (
	id text primary key,
	project_id text,
	parent_id text,
	title text,
	directory text,
	version text,
	agent text,
	model text,
	cost real default 0,
	tokens_input integer default 0,
	tokens_output integer default 0,
	tokens_cache_read integer default 0,
	tokens_cache_write integer default 0,
	time_created integer,
	time_updated integer,
	time_archived integer
);
create table session_message (
	id text primary key,
	session_id text,
	type text,
	seq integer,
	time_created integer,
	time_updated integer,
	data text
);`

type testSession struct {
	id, project, parent, title, dir, version, model string
	cost                                            float64
	in, out, cached, written                        int64
	created, updated                                time.Time
	archived                                        *time.Time
}

func newSession(id string, updated time.Time) testSession {
	return testSession{
		id: id, project: "C:/code/app", title: "Fix the build", dir: "C:/code/app",
		version: "2.2.5", model: `{"id":"model-x","providerID":"opencode"}`,
		created: updated.Add(-time.Hour), updated: updated,
	}
}

func (s testSession) insert(t *testing.T, db *sql.DB) {
	t.Helper()
	var archived any
	if s.archived != nil {
		archived = s.archived.UnixMilli()
	}
	_, err := db.Exec(`
		insert into session_v2(id, project_id, parent_id, title, directory, version, agent, model,
			cost, tokens_input, tokens_output, tokens_cache_read, tokens_cache_write,
			time_created, time_updated, time_archived)
		values (?, ?, nullif(?, ''), ?, ?, ?, 'build', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.id, s.project, s.parent, s.title, s.dir, s.version, s.model, s.cost,
		s.in, s.out, s.cached, s.written, s.created.UnixMilli(), s.updated.UnixMilli(), archived)
	if err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return db, path
}

func addMessage(t *testing.T, db *sql.DB, session, id, typ string, seq int64, updated time.Time, data string) {
	t.Helper()
	_, err := db.Exec(`insert into session_message(id, session_id, type, seq, time_created, time_updated, data)
		values (?, ?, ?, ?, ?, ?, ?)`,
		id, session, typ, seq, updated.UnixMilli(), updated.UnixMilli(), data)
	if err != nil {
		t.Fatal(err)
	}
}

func editMessage(t *testing.T, db *sql.DB, id string, updated time.Time, data string) {
	t.Helper()
	_, err := db.Exec(`update session_message set data = ?, time_updated = ? where id = ?`,
		data, updated.UnixMilli(), id)
	if err != nil {
		t.Fatal(err)
	}
}

func scanOne(t *testing.T, path string) *claude.Session {
	t.Helper()
	sc := &Scanner{path: path, states: map[string]*sessionState{}}
	t.Cleanup(func() { sc.Close() })
	ss, err := sc.Scan(time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want 1", len(ss))
	}
	return ss[0]
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func userData(text string, at time.Time) string {
	return mustJSON(map[string]any{
		"time": map[string]any{"created": at.UnixMilli()},
		"text": text,
	})
}

func assistantData(at time.Time, content ...map[string]any) string {
	return mustJSON(map[string]any{
		"time":    map[string]any{"created": at.UnixMilli(), "completed": at.Add(time.Second).UnixMilli()},
		"agent":   "build",
		"model":   map[string]any{"id": "model-x", "providerID": "opencode"},
		"content": content,
	})
}

func shellState(command, status string, exit *int) map[string]any {
	state := map[string]any{"status": status, "input": map[string]any{"command": command}}
	if status != "running" {
		state["content"] = []map[string]any{{"type": "text", "text": "$ " + command + "\nok"}}
	}
	if exit != nil {
		state["metadata"] = map[string]any{"exit": *exit}
	}
	return state
}

func tool(id, name string, state map[string]any, at time.Time) map[string]any {
	completed := at.Add(time.Second).UnixMilli()
	p := map[string]any{"type": "tool", "id": id, "name": name, "state": state,
		"time": map[string]any{"created": at.UnixMilli()}}
	if state["status"] != "running" {
		p["time"].(map[string]any)["completed"] = completed
	}
	return p
}

func TestSession(t *testing.T) {
	db, path := fixture(t)
	now := time.Now()
	s := newSession("ses_1", now)
	s.in, s.out, s.cached, s.written = 100, 20, 300, 40
	s.cost = 0.25
	s.insert(t, db)

	addMessage(t, db, s.id, "m1", "user", 1, now.Add(-3*time.Minute), userData("fix the build", now.Add(-3*time.Minute)))
	addMessage(t, db, s.id, "m2", "assistant", 2, now.Add(-2*time.Minute), assistantData(now.Add(-2*time.Minute),
		tool("call_1", "shell", shellState("go build ./...", "completed", new(int)), now.Add(-2*time.Minute)),
		tool("call_2", "read", map[string]any{"status": "completed", "input": map[string]any{"filePath": "a.go"}}, now.Add(-2*time.Minute)),
	))
	addMessage(t, db, s.id, "m3", "assistant", 3, now.Add(-time.Minute), assistantData(now.Add(-time.Minute),
		tool("call_3", "shell", shellState("go vet ./...", "completed", new(int)), now.Add(-time.Minute)),
	))
	// A failed command carries its exit code.
	vet := assistantData(now.Add(-30*time.Second), tool("call_4", "shell", shellState("go test ./...", "completed", intp(1)), now.Add(-30*time.Second)))
	addMessage(t, db, s.id, "m4", "assistant", 4, now.Add(-30*time.Second), vet)

	got := scanOne(t, path)
	if got.Agent != Agent || got.ID != s.id || got.Title != "Fix the build" {
		t.Errorf("Agent, ID, Title = %q, %q, %q", got.Agent, got.ID, got.Title)
	}
	if got.Cwd != filepath.FromSlash(s.dir) || got.Model != "model-x" || got.Version != "2.2.5" {
		t.Errorf("Cwd, Model, Version = %q, %q, %q", got.Cwd, got.Model, got.Version)
	}
	if got.Resume != "opencode --session "+s.id || got.Entrypoint != "opencode" {
		t.Errorf("Resume, Entrypoint = %q, %q", got.Resume, got.Entrypoint)
	}
	if got.Prompts != 1 || got.FirstAsk != "fix the build" || got.LastPrompt != "fix the build" {
		t.Errorf("Prompts, FirstAsk, LastPrompt = %d, %q, %q", got.Prompts, got.FirstAsk, got.LastPrompt)
	}
	if got.Turns != 3 {
		t.Errorf("Turns = %d, want 3", got.Turns)
	}
	if got.InputTokens != 440 || got.OutputTokens != 20 || got.CostUSD != 0.25 {
		t.Errorf("tokens, cost = %d, %d, %v", got.InputTokens, got.OutputTokens, got.CostUSD)
	}
	if got.ToolCounts["shell"] != 3 || got.ToolCounts["read"] != 1 {
		t.Errorf("ToolCounts = %v", got.ToolCounts)
	}
	if len(got.Execs) != 3 {
		t.Fatalf("got %d execs", len(got.Execs))
	}
	first := got.Execs[0]
	if first.ToolUseID != "call_1" || first.Command != "go build ./..." || !first.Done() || first.IsError {
		t.Errorf("first exec = %+v", first)
	}
	last := got.Execs[2]
	if !last.IsError || !strings.HasPrefix(last.Output, "Exit code 1") {
		t.Errorf("failed exec = %+v", last)
	}
	if got.StartedAt.UnixMilli() != s.created.UnixMilli() || got.ModTime.UnixMilli() != s.updated.UnixMilli() {
		t.Errorf("times = %v, %v", got.StartedAt, got.ModTime)
	}
}

func intp(n int) *int { return &n }

func TestBackgroundTask(t *testing.T) {
	db, path := fixture(t)
	now := time.Now()
	s := newSession("ses_1", now)
	s.insert(t, db)

	state := map[string]any{
		"status": "completed",
		"input":  map[string]any{"command": "npm run dev", "background": true},
		"content": []map[string]any{{"type": "text", "text": "Command moved to the background (shell ID: sh_1).\n" +
			"Output is streaming to: C:\\logs\\sh_1.out"}},
		"metadata": map[string]any{"status": "running", "shellID": "sh_1"},
	}
	addMessage(t, db, s.id, "m1", "assistant", 1, now.Add(-2*time.Minute),
		assistantData(now.Add(-2*time.Minute), tool("call_1", "shell", state, now.Add(-2*time.Minute))))
	addMessage(t, db, s.id, "m2", "synthetic", 2, now.Add(-time.Minute), mustJSON(map[string]any{
		"time":     map[string]any{"created": now.Add(-time.Minute).UnixMilli()},
		"text":     `<shell id="sh_1" state="completed" command="npm run dev">ready</shell>`,
		"metadata": map[string]any{"source": "shell", "shellID": "sh_1", "state": "completed", "exit": 1},
	}))

	got := scanOne(t, path)
	if len(got.Tasks) != 1 {
		t.Fatalf("got %d tasks", len(got.Tasks))
	}
	task := got.Tasks[0]
	if task.ID != "sh_1" || task.Command != "npm run dev" || task.ToolUseID != "call_1" {
		t.Errorf("task = %+v", task)
	}
	if task.OutputFile != `C:\logs\sh_1.out` {
		t.Errorf("OutputFile = %q", task.OutputFile)
	}
	if task.ExitCode == nil || *task.ExitCode != 1 || task.State(got, time.Now()) != claude.TaskFailed {
		t.Errorf("finished task = %+v", task)
	}
	if task.End.UnixMilli() != now.Add(-time.Minute).UnixMilli() {
		t.Errorf("End = %v", task.End)
	}
	if e := got.Execs[0]; !e.Background || e.TaskID != "sh_1" {
		t.Errorf("exec = %+v", e)
	}
}

func TestIncremental(t *testing.T) {
	db, path := fixture(t)
	now := time.Now()
	s := newSession("ses_1", now)
	s.insert(t, db)
	running := assistantData(now.Add(-2*time.Minute),
		tool("call_1", "shell", shellState("sleep 100", "running", nil), now.Add(-2*time.Minute)))
	addMessage(t, db, s.id, "m1", "assistant", 1, now.Add(-2*time.Minute), running)

	sc := &Scanner{path: path, states: map[string]*sessionState{}}
	t.Cleanup(func() { sc.Close() })
	scan := func() *claude.Session {
		t.Helper()
		ss, err := sc.Scan(time.Hour, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 {
			t.Fatalf("got %d sessions", len(ss))
		}
		return ss[0]
	}

	first := scan()
	if e := first.Execs[0]; e.Done() {
		t.Errorf("running exec = %+v", e)
	}
	if first.Turns != 1 {
		t.Errorf("Turns = %d", first.Turns)
	}

	// The same message is rewritten when its tool call finishes.
	done := assistantData(now.Add(-2*time.Minute),
		tool("call_1", "shell", shellState("sleep 100", "completed", new(int)), now.Add(-2*time.Minute)))
	editMessage(t, db, "m1", now.Add(-time.Minute), done)
	if _, err := db.Exec(`update session_v2 set time_updated = ? where id = ?`, now.Add(-time.Minute).UnixMilli(), s.id); err != nil {
		t.Fatal(err)
	}
	second := scan()
	if e := second.Execs[0]; !e.Done() || e.IsError {
		t.Errorf("finished exec = %+v", e)
	}
	if second.Turns != 1 {
		t.Errorf("after rewrite: Turns = %d, want 1", second.Turns)
	}

	// A new message adds to the totals; a removed one takes its tools with it.
	addMessage(t, db, s.id, "m2", "user", 2, now.Add(-30*time.Second), userData("next", now.Add(-30*time.Second)))
	if _, err := db.Exec(`update session_v2 set time_updated = ? where id = ?`, now.Add(-30*time.Second).UnixMilli(), s.id); err != nil {
		t.Fatal(err)
	}
	third := scan()
	if third.Prompts != 1 || third.Turns != 1 || len(third.Execs) != 1 {
		t.Errorf("after add: prompts %d, turns %d, execs %d", third.Prompts, third.Turns, len(third.Execs))
	}

	if _, err := db.Exec(`delete from session_message where id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`update session_v2 set time_updated = ? where id = ?`, now.UnixMilli(), s.id); err != nil {
		t.Fatal(err)
	}
	fourth := scan()
	if fourth.Turns != 0 || len(fourth.Execs) != 0 {
		t.Errorf("after delete: turns %d, execs %d", fourth.Turns, len(fourth.Execs))
	}
}

// A snapshot handed out must not change as the scanner reads further.
func TestSnapshotIsolated(t *testing.T) {
	db, path := fixture(t)
	now := time.Now()
	s := newSession("ses_1", now)
	s.insert(t, db)
	addMessage(t, db, s.id, "m1", "assistant", 1, now.Add(-2*time.Minute),
		assistantData(now.Add(-2*time.Minute), tool("call_1", "shell", shellState("ls", "running", nil), now.Add(-2*time.Minute))))

	sc := &Scanner{path: path, states: map[string]*sessionState{}}
	t.Cleanup(func() { sc.Close() })
	before, _ := sc.Scan(time.Hour, 0)
	editMessage(t, db, "m1", now, assistantData(now.Add(-2*time.Minute),
		tool("call_1", "shell", shellState("ls", "completed", new(int)), now.Add(-2*time.Minute))))
	db.Exec(`update session_v2 set time_updated = ? where id = ?`, now.UnixMilli(), s.id)
	sc.Scan(time.Hour, 0)
	if before[0].Execs[0].Done() {
		t.Errorf("earlier snapshot changed: %+v", before[0].Execs[0])
	}
}

// A tool finishing rewrites its message; the session row is not always
// touched at the same time.
func TestMessageRewriteWithoutSessionUpdate(t *testing.T) {
	db, path := fixture(t)
	now := time.Now()
	at := now.Add(-2 * time.Minute)
	s := newSession("ses_1", now)
	s.insert(t, db)
	addMessage(t, db, s.id, "m1", "assistant", 1, at,
		assistantData(at, tool("call_1", "shell", shellState("sleep 100", "running", nil), at)))

	sc := &Scanner{path: path, states: map[string]*sessionState{}}
	t.Cleanup(func() { sc.Close() })
	first, err := sc.Scan(time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Execs[0].Done() {
		t.Fatalf("first scan: exec already done")
	}

	editMessage(t, db, "m1", now, assistantData(at,
		tool("call_1", "shell", shellState("sleep 100", "completed", new(int)), at)))
	second, err := sc.Scan(time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !second[0].Execs[0].Done() {
		t.Errorf("message rewrite missed: exec still running")
	}
}

func TestInterruptedWhenIdle(t *testing.T) {
	db, path := fixture(t)
	old := time.Now().Add(-10 * time.Minute)
	s := newSession("ses_1", old)
	s.insert(t, db)
	addMessage(t, db, s.id, "m1", "assistant", 1, old,
		assistantData(old, tool("call_1", "shell", shellState("sleep 100", "running", nil), old)))

	got := scanOne(t, path)
	if e := got.Execs[0]; !e.Done() || !e.Interrupted {
		t.Errorf("idle exec = %+v", e)
	}
}

func TestSkipsChildAndArchivedSessions(t *testing.T) {
	db, path := fixture(t)
	now := time.Now()

	child := newSession("ses_child", now)
	child.parent = "ses_parent"
	child.insert(t, db)
	addMessage(t, db, child.id, "child_m1", "user", 1, now, userData("subagent", now))

	archived := now.Add(-time.Minute)
	old := newSession("ses_archived", now)
	old.archived = &archived
	old.insert(t, db)
	addMessage(t, db, old.id, "old_m1", "user", 1, now, userData("archived", now))

	parent := newSession("ses_parent", now)
	parent.insert(t, db)
	addMessage(t, db, parent.id, "parent_m1", "user", 1, now, userData("keep me", now))

	sc := &Scanner{path: path, states: map[string]*sessionState{}}
	t.Cleanup(func() { sc.Close() })
	ss, err := sc.Scan(time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "ses_parent" {
		t.Errorf("sessions = %v", ids(ss))
	}
}

// Sessions outside the range are not read and stale state is dropped.
func TestRange(t *testing.T) {
	db, path := fixture(t)
	old := time.Now().Add(-20 * 24 * time.Hour)
	s := newSession("ses_old", old)
	s.insert(t, db)
	addMessage(t, db, s.id, "m1", "user", 1, old, userData("old", old))

	sc := &Scanner{path: path, states: map[string]*sessionState{}}
	t.Cleanup(func() { sc.Close() })
	if ss, err := sc.Scan(7*24*time.Hour, 0); err != nil || len(ss) != 0 {
		t.Errorf("7d: %v, %v", ids(ss), err)
	}
	ss, err := sc.Scan(30*24*time.Hour, 0)
	if err != nil || len(ss) != 1 {
		t.Fatalf("30d: %v, %v", ids(ss), err)
	}
	sc.Scan(7*24*time.Hour, 0)
	if len(sc.states) != 0 {
		t.Errorf("kept %d out-of-range sessions", len(sc.states))
	}
}

func TestMissingDatabase(t *testing.T) {
	sc := &Scanner{path: filepath.Join(t.TempDir(), "nope", "opencode.db"), states: map[string]*sessionState{}}
	ss, err := sc.Scan(time.Hour, 0)
	if err != nil || len(ss) != 0 {
		t.Errorf("Scan = %v, %v", ss, err)
	}

	// An empty SQLite database is not an OpenCode database.
	db, path := fixture(t)
	if _, err := db.Exec(`drop table session_v2`); err != nil {
		t.Fatal(err)
	}
	sc = &Scanner{path: path, states: map[string]*sessionState{}}
	t.Cleanup(func() { sc.Close() })
	if ss, err := sc.Scan(time.Hour, 0); err != nil || len(ss) != 0 {
		t.Errorf("no schema: %v, %v", ss, err)
	}
}

func TestDBPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("OPENCODE_TUI_CHANNEL", "")
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))

	if got, want := DataDir(), filepath.Join(home, "xdg", "opencode"); got != want {
		t.Errorf("DataDir = %q, want %q", got, want)
	}
	if got, want := DBPath(), filepath.Join(home, "xdg", "opencode", "opencode.db"); got != want {
		t.Errorf("DBPath = %q, want %q", got, want)
	}

	t.Setenv("OPENCODE_DB", "other.db")
	if got, want := DBPath(), filepath.Join(DataDir(), "other.db"); got != want {
		t.Errorf("relative OPENCODE_DB = %q, want %q", got, want)
	}
	abs := filepath.Join(home, "elsewhere.db")
	t.Setenv("OPENCODE_DB", abs)
	if got := DBPath(); got != abs {
		t.Errorf("absolute OPENCODE_DB = %q, want %q", got, abs)
	}
	t.Setenv("OPENCODE_DB", ":memory:")
	if got := DBPath(); got != "" {
		t.Errorf("memory OPENCODE_DB = %q", got)
	}

	// A channel keeps its database under its own name.
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("OPENCODE_TUI_CHANNEL", "next")
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(DataDir(), "opencode-next.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := DBPath(), filepath.Join(DataDir(), "opencode-next.db"); got != want {
		t.Errorf("channel DBPath = %q, want %q", got, want)
	}
}

func ids(ss []*claude.Session) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}
