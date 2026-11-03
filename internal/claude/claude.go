// Package claude discovers Claude Code sessions on this machine by reading
// the transcripts Claude Code keeps under ~/.claude/projects. It is
// read-only: it never writes to Claude's files.
package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/proc"
)

// ActiveWindow is how recently a transcript must have changed for its
// session to count as active.
const ActiveWindow = 2 * time.Minute

// Agent is the name recorded on runs and sessions that came from Claude Code.
const Agent = "claude-code"

type Session struct {
	Agent      string
	ID         string
	Path       string
	Project    string // project directory slug under ~/.claude/projects
	Cwd        string
	Branch     string
	Version    string
	Entrypoint string
	Title      string
	FirstAsk   string
	LastPrompt string
	Model      string
	Resume     string // the command that continues this session

	StartedAt    time.Time
	LastActivity time.Time
	ModTime      time.Time
	Size         int64

	Prompts      int
	Turns        int
	ToolCounts   map[string]int
	CostUSD      float64
	InputTokens  int64
	OutputTokens int64

	Execs []*Exec
	Tasks []*Task
}

func (s *Session) DisplayTitle() string {
	switch {
	case s.Title != "":
		return s.Title
	case s.FirstAsk != "":
		return s.FirstAsk
	}
	return s.ID
}

func (s *Session) Active(now time.Time) bool {
	return now.Sub(s.ModTime) < ActiveWindow
}

func (s *Session) RunningTasks(now time.Time) int {
	n := 0
	for _, t := range s.Tasks {
		if t.State(s, now) == TaskRunning {
			n++
		}
	}
	return n
}

// Exec is one shell command the agent ran (Bash or PowerShell tool call).
type Exec struct {
	ToolUseID   string
	Tool        string
	Command     string
	Description string
	Background  bool
	Start       time.Time
	End         time.Time // zero while pending
	IsError     bool
	Interrupted bool
	Output      string
	TaskID      string
}

func (e *Exec) Done() bool { return !e.End.IsZero() }

func (e *Exec) Duration(now time.Time) time.Duration {
	if e.Start.IsZero() {
		return 0
	}
	if e.End.IsZero() {
		return now.Sub(e.Start)
	}
	return e.End.Sub(e.Start)
}

type TaskState string

const (
	TaskRunning   TaskState = "running"
	TaskCompleted TaskState = "completed"
	TaskFailed    TaskState = "failed"
	TaskKilled    TaskState = "killed"
	TaskUnknown   TaskState = "unknown"
)

// Task is a background task Claude Code started (run_in_background, or a
// command the user pushed to the background).
type Task struct {
	ID          string
	ToolUseID   string
	Command     string
	Description string
	OutputFile  string
	Status      string // as reported by Claude Code's task notification
	Summary     string
	ExitCode    *int
	Start       time.Time
	End         time.Time
}

// State reconciles the notification status with what can be observed: a
// task with no completion notice is running only while its session is
// active or its output file is still being written.
func (t *Task) State(s *Session, now time.Time) TaskState {
	switch t.Status {
	case "completed":
		if t.ExitCode != nil && *t.ExitCode != 0 {
			return TaskFailed
		}
		return TaskCompleted
	case "failed":
		return TaskFailed
	case "killed", "stopped":
		return TaskKilled
	case "":
		if s.Active(now) {
			return TaskRunning
		}
		if fi, err := os.Stat(t.OutputFile); err == nil && now.Sub(fi.ModTime()) < ActiveWindow {
			return TaskRunning
		}
		return TaskUnknown
	}
	return TaskState(t.Status)
}

// Duration is how long the task has run. A task whose fate is unknown is
// measured up to the session's last activity rather than growing forever.
func (t *Task) Duration(s *Session, now time.Time) time.Duration {
	if t.Start.IsZero() {
		return 0
	}
	end := t.End
	if end.IsZero() {
		end = now
		if t.State(s, now) == TaskUnknown && s.ModTime.After(t.Start) {
			end = s.ModTime
		}
	}
	return end.Sub(t.Start)
}

var reCdPrefix = regexp.MustCompile(`^\s*cd\s+("[^"]*"|'[^']*'|[^\s;&|]+)\s*(&&|;)\s*`)

// ShortCommand drops the leading `cd <dir> &&` agents put on most commands,
// for compact display.
func ShortCommand(cmd string) string {
	if short := reCdPrefix.ReplaceAllString(cmd, ""); strings.TrimSpace(short) != "" {
		return short
	}
	return cmd
}

// ConfigDir is Claude Code's config directory (CLAUDE_CONFIG_DIR or ~/.claude).
func ConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude")
}

// LiveProcesses counts running Claude Code processes, from the per-pid
// files Claude Code keeps in its sessions directory.
func LiveProcesses() int {
	entries, err := os.ReadDir(filepath.Join(ConfigDir(), "sessions"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		if pid, err := strconv.Atoi(name); err == nil && proc.Alive(pid) {
			n++
		}
	}
	return n
}

// Scanner parses transcripts incrementally. Transcripts are append-only, so
// each refresh reads only the bytes added since the last one.
type Scanner struct {
	mu    sync.Mutex
	root  string
	files map[string]*fileState
}

type fileState struct {
	offset      int64
	customTitle bool
	sess        *Session
	execs       map[string]*Exec
	tasks       map[string]*Task
	msgSeen     map[string]bool
}

func NewScanner() *Scanner {
	return &Scanner{root: filepath.Join(ConfigDir(), "projects"), files: map[string]*fileState{}}
}

// Scan returns sessions whose transcripts changed within maxAge, most
// recently active first, at most limit of them.
func (sc *Scanner) Scan(maxAge time.Duration, limit int) ([]*Session, error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	paths, err := filepath.Glob(filepath.Join(sc.root, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	type cand struct {
		path string
		fi   os.FileInfo
	}
	now := time.Now()
	var cands []cand
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil || now.Sub(fi.ModTime()) > maxAge {
			continue
		}
		cands = append(cands, cand{p, fi})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].fi.ModTime().After(cands[j].fi.ModTime()) })
	if limit > 0 && len(cands) > limit {
		cands = cands[:limit]
	}

	out := make([]*Session, 0, len(cands))
	for _, c := range cands {
		st := sc.files[c.path]
		if st == nil || c.fi.Size() < st.offset {
			st = newFileState(c.path)
			sc.files[c.path] = st
		}
		if c.fi.Size() > st.offset {
			_ = st.read(c.path)
		}
		st.sess.ModTime, st.sess.Size = c.fi.ModTime(), c.fi.Size()
		if st.sess.Prompts == 0 && st.sess.Turns == 0 {
			continue // empty or metadata-only transcript
		}
		out = append(out, st.sess.Snapshot())
	}
	return out, nil
}

// Snapshot deep-copies a session so callers can read it while the scanner
// keeps updating the original.
func (s *Session) Snapshot() *Session {
	c := *s
	c.ToolCounts = make(map[string]int, len(s.ToolCounts))
	for k, v := range s.ToolCounts {
		c.ToolCounts[k] = v
	}
	c.Execs = make([]*Exec, len(s.Execs))
	for i, e := range s.Execs {
		ec := *e
		c.Execs[i] = &ec
	}
	c.Tasks = make([]*Task, len(s.Tasks))
	for i, t := range s.Tasks {
		tc := *t
		c.Tasks[i] = &tc
	}
	return &c
}

func newFileState(path string) *fileState {
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	return &fileState{
		sess: &Session{
			Agent:      Agent,
			ID:         id,
			Path:       path,
			Project:    filepath.Base(filepath.Dir(path)),
			Resume:     "claude --resume " + id,
			ToolCounts: map[string]int{},
		},
		execs:   map[string]*Exec{},
		tasks:   map[string]*Task{},
		msgSeen: map[string]bool{},
	}
}

func (st *fileState) read(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(st.offset, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReaderSize(f, 256*1024)
	for {
		ln, err := r.ReadBytes('\n')
		if err != nil {
			// A trailing partial line is still being written; pick it up on
			// the next refresh.
			return nil
		}
		st.offset += int64(len(ln))
		st.parse(bytes.TrimSpace(ln))
	}
}

type record struct {
	Type        string          `json:"type"`
	Timestamp   string          `json:"timestamp"`
	Cwd         string          `json:"cwd"`
	GitBranch   string          `json:"gitBranch"`
	Version     string          `json:"version"`
	Entrypoint  string          `json:"entrypoint"`
	IsMeta      bool            `json:"isMeta"`
	IsSidechain bool            `json:"isSidechain"`
	AITitle     string          `json:"aiTitle"`
	CustomTitle string          `json:"customTitle"`
	LastPrompt  string          `json:"lastPrompt"`
	CostUSD     float64         `json:"totalCostUSD"`
	Content     json.RawMessage `json:"content"`
	Message     *struct {
		ID      string          `json:"id"`
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			Input       int64 `json:"input_tokens"`
			Output      int64 `json:"output_tokens"`
			CacheCreate int64 `json:"cache_creation_input_tokens"`
			CacheRead   int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	Attachment    *struct {
		Prompt string `json:"prompt"`
	} `json:"attachment"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type toolResultMeta struct {
	Interrupted      bool   `json:"interrupted"`
	BackgroundTaskID string `json:"backgroundTaskId"`
}

var (
	reOutputFile = regexp.MustCompile(`Output is being written to: (.+?\.output)`)
	reExitCode   = regexp.MustCompile(`exit code (-?\d+)`)
)

func (st *fileState) parse(ln []byte) {
	if len(ln) == 0 {
		return
	}
	var rec record
	if json.Unmarshal(ln, &rec) != nil {
		return
	}
	s := st.sess
	ts, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
	if !ts.IsZero() {
		if s.StartedAt.IsZero() {
			s.StartedAt = ts
		}
		if ts.After(s.LastActivity) {
			s.LastActivity = ts
		}
	}
	if rec.Cwd != "" {
		s.Cwd = rec.Cwd
	}
	if rec.GitBranch != "" {
		s.Branch = rec.GitBranch
	}
	if rec.Version != "" {
		s.Version = rec.Version
	}
	if rec.Entrypoint != "" {
		s.Entrypoint = rec.Entrypoint
	}

	switch rec.Type {
	case "custom-title":
		s.Title, st.customTitle = rec.CustomTitle, true
	case "ai-title":
		if !st.customTitle {
			s.Title = rec.AITitle
		}
	case "last-prompt":
		s.LastPrompt = rec.LastPrompt
	case "cost-state":
		s.CostUSD = rec.CostUSD
	case "queue-operation":
		var text string
		if json.Unmarshal(rec.Content, &text) == nil {
			st.notification(text, ts)
		}
	case "attachment":
		if rec.Attachment != nil {
			st.notification(rec.Attachment.Prompt, ts)
		}
	case "assistant":
		st.assistant(&rec, ts)
	case "user":
		st.user(&rec, ts)
	}
}

func (st *fileState) assistant(rec *record, ts time.Time) {
	m := rec.Message
	if m == nil || rec.IsSidechain {
		return
	}
	s := st.sess
	if m.Model != "" && !strings.HasPrefix(m.Model, "<") {
		s.Model = m.Model
	}
	// One API message is logged as several lines, one per content block.
	if m.ID != "" && !st.msgSeen[m.ID] {
		st.msgSeen[m.ID] = true
		s.Turns++
		if u := m.Usage; u != nil {
			s.InputTokens += u.Input + u.CacheCreate + u.CacheRead
			s.OutputTokens += u.Output
		}
	}
	var blocks []block
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		s.ToolCounts[b.Name]++
		var in struct {
			Command     string `json:"command"`
			Description string `json:"description"`
			Background  bool   `json:"run_in_background"`
		}
		if json.Unmarshal(b.Input, &in) != nil || in.Command == "" {
			continue
		}
		if _, dup := st.execs[b.ID]; dup {
			continue
		}
		e := &Exec{
			ToolUseID:   b.ID,
			Tool:        b.Name,
			Command:     in.Command,
			Description: in.Description,
			Background:  in.Background,
			Start:       ts,
		}
		st.execs[b.ID] = e
		s.Execs = append(s.Execs, e)
	}
}

func (st *fileState) user(rec *record, ts time.Time) {
	m := rec.Message
	if m == nil || rec.IsSidechain {
		return
	}
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		if strings.Contains(text, "<task-notification>") {
			st.notification(text, ts)
			return
		}
		if !rec.IsMeta && !strings.HasPrefix(strings.TrimSpace(text), "<") {
			st.prompt(text)
		}
		return
	}
	var blocks []block
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	prompted := false
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if !rec.IsMeta && !prompted && !strings.HasPrefix(strings.TrimSpace(b.Text), "<") {
				st.prompt(b.Text)
				prompted = true
			}
		case "tool_result":
			e := st.execs[b.ToolUseID]
			if e == nil {
				continue
			}
			e.End, e.IsError = ts, b.IsError
			e.Output = Clip(blockText(b.Content), 8000)
			var meta toolResultMeta
			_ = json.Unmarshal(rec.ToolUseResult, &meta)
			e.Interrupted = meta.Interrupted
			if meta.BackgroundTaskID != "" {
				e.Background = true
				e.TaskID = meta.BackgroundTaskID
				t := st.task(meta.BackgroundTaskID, ts)
				t.ToolUseID, t.Command, t.Description = e.ToolUseID, e.Command, e.Description
				if t.Start.IsZero() || e.Start.Before(t.Start) {
					t.Start = e.Start
				}
				if mm := reOutputFile.FindStringSubmatch(e.Output); mm != nil {
					t.OutputFile = mm[1]
				}
			}
		}
	}
}

func (st *fileState) prompt(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s := st.sess
	s.Prompts++
	if s.FirstAsk == "" {
		s.FirstAsk = OneLine(text, 120)
	}
}

func (st *fileState) task(id string, ts time.Time) *Task {
	t := st.tasks[id]
	if t == nil {
		t = &Task{ID: id, Start: ts}
		st.tasks[id] = t
		st.sess.Tasks = append(st.sess.Tasks, t)
	}
	return t
}

func (st *fileState) notification(text string, ts time.Time) {
	if !strings.Contains(text, "<task-notification>") {
		return
	}
	id := tag(text, "task-id")
	if id == "" {
		return
	}
	t := st.task(id, ts)
	if f := tag(text, "output-file"); f != "" {
		t.OutputFile = f
	}
	status := tag(text, "status")
	if status == "" || t.Status == status {
		return
	}
	t.Status = status
	t.Summary = tag(text, "summary")
	if t.End.IsZero() || ts.Before(t.End) {
		t.End = ts
	}
	if m := reExitCode.FindStringSubmatch(t.Summary); m != nil {
		if c, err := strconv.Atoi(m[1]); err == nil {
			t.ExitCode = &c
		}
	}
	if t.Description == "" && t.Summary != "" {
		t.Description = t.Summary
	}
}

func tag(s, name string) string {
	open, close := "<"+name+">", "</"+name+">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

func blockText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []block
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// Clip keeps the head and tail of long output, where errors usually are.
func Clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	half := max / 2
	return s[:half] + "\n\n  … " + strconv.Itoa(len(s)-max) + " bytes omitted …\n\n" + s[len(s)-half:]
}

func OneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
