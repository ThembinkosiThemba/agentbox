// Package store persists agentbox runs on disk. There is no daemon: every run
// lives in its own directory under the agentbox home, and the supervisor for a
// run is the only process that writes its meta.json.
package store

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusKilled    Status = "killed"
	StatusTimedOut  Status = "timeout"
	// StatusLost is never written to disk. It is derived when a run claims to
	// be running but its supervisor has stopped sending heartbeats.
	StatusLost Status = "lost"
)

// Terminal reports whether the status is final.
func (s Status) Terminal() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusKilled, StatusTimedOut, StatusLost:
		return true
	}
	return false
}

// HeartbeatInterval is how often a supervisor touches its heartbeat file.
// A running run whose heartbeat is older than LostAfter is reported as lost.
const (
	HeartbeatInterval = 2 * time.Second
	LostAfter         = 15 * time.Second
)

type Run struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Title is a human-readable description of what the run is for, e.g.
	// "Run the e2e suite". It is only shown, never used to find the run.
	Title   string   `json:"title,omitempty"`
	Command []string `json:"command"`
	// UseShell runs Command through the platform shell instead of exec'ing it.
	UseShell bool `json:"use_shell"`
	// Shell holds the interpreter prefix when the command runs through a
	// shell, e.g. ["bash", "-c"]. Empty means the command is exec'd directly.
	Shell []string `json:"shell,omitempty"`
	Cwd   string   `json:"cwd"`

	Status    Status     `json:"status"`
	ExitCode  *int       `json:"exit_code,omitempty"`
	Error     string     `json:"error,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`

	PID           int   `json:"pid,omitempty"`
	SupervisorPID int   `json:"supervisor_pid,omitempty"`
	Detached      bool  `json:"detached"`
	TimeoutSec    int64 `json:"timeout_sec,omitempty"`

	// Who launched it. Agent is e.g. "claude-code"; empty for a human.
	Agent     string `json:"agent,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	AgentPID  int    `json:"agent_pid,omitempty"`

	RestartOf string `json:"restart_of,omitempty"`
}

// DisplayName is the title, else the name, else the command line.
func (r *Run) DisplayName() string {
	if r.Title != "" {
		return r.Title
	}
	if r.Name != "" {
		return r.Name
	}
	return r.CommandLine()
}

func (r *Run) CommandLine() string {
	if len(r.Command) == 1 {
		return r.Command[0]
	}
	parts := make([]string, len(r.Command))
	for i, a := range r.Command {
		if a == "" || strings.ContainsAny(a, " \t\"'") {
			a = fmt.Sprintf("%q", a)
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

// Duration is wall time so far for a running run, or total for a finished one.
func (r *Run) Duration(now time.Time) time.Duration {
	if r.StartedAt == nil {
		return 0
	}
	end := now
	if r.EndedAt != nil {
		end = *r.EndedAt
	}
	return end.Sub(*r.StartedAt)
}

// Home returns the agentbox state directory, creating it if needed.
// AGENTBOX_HOME overrides the platform default.
func Home() (string, error) {
	dir := os.Getenv("AGENTBOX_HOME")
	if dir == "" {
		switch runtime.GOOS {
		case "windows":
			base := os.Getenv("LOCALAPPDATA")
			if base == "" {
				var err error
				if base, err = os.UserConfigDir(); err != nil {
					return "", err
				}
			}
			dir = filepath.Join(base, "agentbox")
		default:
			base := os.Getenv("XDG_STATE_HOME")
			if base == "" {
				h, err := os.UserHomeDir()
				if err != nil {
					return "", err
				}
				base = filepath.Join(h, ".local", "state")
			}
			dir = filepath.Join(base, "agentbox")
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "runs"), 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

type Store struct {
	root string
}

func Open() (*Store, error) {
	h, err := Home()
	if err != nil {
		return nil, err
	}
	return &Store{root: filepath.Join(h, "runs")}, nil
}

func (s *Store) Root() string               { return s.root }
func (s *Store) Dir(id string) string       { return filepath.Join(s.root, id) }
func (s *Store) LogPath(id string) string   { return filepath.Join(s.root, id, "output.log") }
func (s *Store) metaPath(id string) string  { return filepath.Join(s.root, id, "meta.json") }
func (s *Store) killPath(id string) string  { return filepath.Join(s.root, id, "kill") }
func (s *Store) heartPath(id string) string { return filepath.Join(s.root, id, "heartbeat") }

const idAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

func newID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = idAlphabet[int(b[i])%len(idAlphabet)]
	}
	return string(b)
}

// Create allocates an id and directory for r and writes its first meta.json.
func (s *Store) Create(r *Run) error {
	for range 10 {
		id := newID()
		if err := os.Mkdir(s.Dir(id), 0o755); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return err
		}
		r.ID = id
		if r.CreatedAt.IsZero() {
			r.CreatedAt = time.Now()
		}
		if r.Status == "" {
			r.Status = StatusPending
		}
		return s.Save(r)
	}
	return errors.New("could not allocate a run id")
}

// Save writes meta.json atomically. Renames can briefly fail on Windows while
// a reader holds the file open, so it retries.
func (s *Store) Save(r *Run) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.metaPath(r.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	for i := 0; ; i++ {
		err = os.Rename(tmp, s.metaPath(r.ID))
		if err == nil || i == 50 {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Load reads one run and resolves derived state (lost runs).
func (s *Store) Load(id string) (*Run, error) {
	var data []byte
	var err error
	for i := 0; i < 5; i++ {
		if data, err = os.ReadFile(s.metaPath(id)); err == nil {
			break
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("run %s not found", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("run %s: corrupt meta: %w", id, err)
	}
	s.derive(&r)
	return &r, nil
}

func (s *Store) derive(r *Run) {
	if r.Status != StatusRunning && r.Status != StatusPending {
		return
	}
	ref := r.CreatedAt
	if fi, err := os.Stat(s.heartPath(r.ID)); err == nil {
		ref = fi.ModTime()
	}
	if time.Since(ref) > LostAfter {
		r.Status = StatusLost
	}
}

// List returns all runs, newest first.
func (s *Store) List() ([]*Run, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	runs := make([]*Run, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		r, err := s.Load(e.Name())
		if err != nil {
			continue
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.After(runs[j].CreatedAt) })
	return runs, nil
}

// Resolve finds a run by full id, unique id prefix, or exact name (most
// recent wins for names).
func (s *Store) Resolve(ref string) (*Run, error) {
	if ref == "" {
		return nil, errors.New("missing run id")
	}
	if r, err := s.Load(ref); err == nil {
		return r, nil
	}
	runs, err := s.List()
	if err != nil {
		return nil, err
	}
	var matches []*Run
	for _, r := range runs {
		if strings.HasPrefix(r.ID, ref) {
			matches = append(matches, r)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("%q matches %d runs; use more characters", ref, len(matches))
	}
	for _, r := range runs {
		if r.Name == ref {
			return r, nil
		}
	}
	return nil, fmt.Errorf("run %q not found", ref)
}

func (s *Store) Delete(id string) error { return os.RemoveAll(s.Dir(id)) }

func (s *Store) Heartbeat(id string) error {
	p := s.heartPath(id)
	now := time.Now()
	if err := os.Chtimes(p, now, now); err != nil {
		return os.WriteFile(p, nil, 0o644)
	}
	return nil
}

// RequestKill leaves a marker the supervisor polls for.
func (s *Store) RequestKill(id string) error {
	return os.WriteFile(s.killPath(id), []byte(time.Now().Format(time.RFC3339)), 0o644)
}

func (s *Store) KillRequested(id string) bool {
	_, err := os.Stat(s.killPath(id))
	return err == nil
}

// ExitText is the exit code worth showing, or "" when there is none or it
// is just an artifact of the run being stopped.
func (r *Run) ExitText() string {
	if r.ExitCode == nil || (r.Status != StatusSucceeded && r.Status != StatusFailed) {
		return ""
	}
	c := *r.ExitCode
	if c > 255 || c < 0 {
		return fmt.Sprintf("0x%X", uint32(c))
	}
	return fmt.Sprint(c)
}
