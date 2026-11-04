package supervisor

import (
	"os"
	"strings"
	"testing"
	"time"

	"agentbox/internal/store"
)

// The test binary doubles as the child process, so these tests need no
// shell or external commands and behave the same on Windows and Linux.
func TestMain(m *testing.M) {
	switch os.Getenv("AGENTBOX_TEST_CHILD") {
	case "":
		os.Exit(m.Run())
	case "echo":
		os.Stdout.WriteString("hello from child\n")
		os.Stderr.WriteString("to stderr\n")
		os.Exit(3)
	case "sleep":
		os.Stdout.WriteString("sleeping\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

func setup(t *testing.T, child string) (*store.Store, *store.Run) {
	t.Setenv("AGENTBOX_HOME", t.TempDir())
	t.Setenv("AGENTBOX_TEST_CHILD", child)
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Create(st, Options{Argv: []string{os.Args[0]}, Name: child})
	if err != nil {
		t.Fatal(err)
	}
	return st, r
}

func TestSuperviseRecordsOutcome(t *testing.T) {
	st, r := setup(t, "echo")
	code, err := Supervise(st, r.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	got, _ := st.Load(r.ID)
	if got.Status != store.StatusFailed || got.ExitText() != "3" || got.EndedAt == nil {
		t.Fatalf("unexpected run: %+v", got)
	}
	log, _ := os.ReadFile(st.LogPath(r.ID))
	if !strings.Contains(string(log), "hello from child") || !strings.Contains(string(log), "to stderr") {
		t.Fatalf("log missing output: %q", log)
	}
}

func TestKillStopsRun(t *testing.T) {
	st, r := setup(t, "sleep")
	done := make(chan int, 1)
	go func() {
		code, _ := Supervise(st, r.ID, false)
		done <- code
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		cur, _ := st.Load(r.ID)
		if cur != nil && cur.Status == store.StatusRunning {
			r = cur
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, err := Kill(st, r)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusKilled {
		t.Fatalf("status = %s, want killed", got.Status)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("supervisor did not return after kill")
	}
}

func TestTimeout(t *testing.T) {
	st, r := setup(t, "sleep")
	r.TimeoutSec = 1
	if err := st.Save(r); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := Supervise(st, r.ID, false); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Load(r.ID)
	if got.Status != store.StatusTimedOut {
		t.Fatalf("status = %s, want timeout", got.Status)
	}
	if d := time.Since(start); d > KillGrace+5*time.Second {
		t.Fatalf("took %s to time out", d)
	}
}

func TestAttribute(t *testing.T) {
	for _, v := range []string{"AGENTBOX_AGENT", "CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_PID",
		"ANTIGRAVITY_CONVERSATION_ID", "OPENCODE", "OPENCODE_SESSION_ID", "AI_AGENT"} {
		t.Setenv(v, "")
	}
	r := &store.Run{}
	attribute(r)
	if r.Agent != "" {
		t.Errorf("no agent: Agent = %q", r.Agent)
	}

	t.Setenv("ANTIGRAVITY_CONVERSATION_ID", "41fec8a8")
	r = &store.Run{}
	attribute(r)
	if r.Agent != "antigravity" || r.SessionID != "41fec8a8" {
		t.Errorf("agy: Agent, SessionID = %q, %q", r.Agent, r.SessionID)
	}
	t.Setenv("ANTIGRAVITY_CONVERSATION_ID", "")

	t.Setenv("OPENCODE_SESSION_ID", "ses_1")
	r = &store.Run{}
	attribute(r)
	if r.Agent != "opencode" || r.SessionID != "ses_1" {
		t.Errorf("opencode: Agent, SessionID = %q, %q", r.Agent, r.SessionID)
	}
	t.Setenv("OPENCODE_SESSION_ID", "")

	// OpenCode also exports OPENCODE=1, so a run started from it is still
	// recognised when the session id is not set.
	t.Setenv("OPENCODE", "1")
	r = &store.Run{}
	attribute(r)
	if r.Agent != "opencode" || r.SessionID != "" {
		t.Errorf("opencode without session: Agent, SessionID = %q, %q", r.Agent, r.SessionID)
	}
	t.Setenv("OPENCODE", "")

	t.Setenv("AGENTBOX_AGENT", "mine")
	r = &store.Run{}
	attribute(r)
	if r.Agent != "mine" {
		t.Errorf("explicit: Agent = %q", r.Agent)
	}
}
