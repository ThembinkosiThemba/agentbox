// Package agy discovers Antigravity CLI conversations by reading the
// transcripts agy keeps under ~/.gemini/antigravity-cli. It is read-only: it
// never writes to agy's files.
package agy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/claude"
)

// Agent is the name recorded on runs and sessions that came from agy.
const Agent = "antigravity"

// DataDir is where agy keeps its conversations.
func DataDir() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".gemini", "antigravity-cli")
}

// Scanner parses transcripts incrementally. Transcripts are append-only, so
// each refresh reads only the bytes added since the last one.
type Scanner struct {
	mu    sync.Mutex
	root  string
	files map[string]*fileState

	conversations cached[conversationCache]
	workspaces    cached[map[string]string] // workspace path -> latest conversation
}

// conversationCache is agy's cache/conversation_metadata.json. It holds only
// some conversations, so everything read from it is optional.
type conversationCache struct {
	Conversations map[string]struct {
		Summary struct {
			Title         string
			Preview       string
			WorkspaceURIs []string
		} `json:"summary"`
		IsInternal bool `json:"is_internal"`
	} `json:"conversations"`
}

// cached is a JSON file that is parsed again only after it changes.
type cached[T any] struct {
	mod time.Time
	val T
}

func (c *cached[T]) load(path string) {
	fi, err := os.Stat(path)
	if err != nil || fi.ModTime().Equal(c.mod) {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var v T
	if json.Unmarshal(data, &v) == nil {
		c.mod, c.val = fi.ModTime(), v
	}
}

type fileState struct {
	offset  int64
	sess    *claude.Session
	cmdCwd  string
	execs   map[string]*claude.Exec
	tasks   map[string]*claude.Task
	pending map[int]*claude.Exec // by the step its result will have
	early   map[int]*step        // results written before their tool call
}

func NewScanner() *Scanner {
	return &Scanner{root: DataDir(), files: map[string]*fileState{}}
}

// Scan returns conversations whose transcripts changed within maxAge, most
// recently active first, at most limit of them.
func (sc *Scanner) Scan(maxAge time.Duration, limit int) ([]*claude.Session, error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	paths, err := filepath.Glob(filepath.Join(sc.root, "brain", "*", ".system_generated", "logs", "transcript_full.jsonl"))
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

	sc.conversations.load(filepath.Join(sc.root, "cache", "conversation_metadata.json"))
	sc.workspaces.load(filepath.Join(sc.root, "cache", "last_conversations.json"))

	out := make([]*claude.Session, 0, len(cands))
	for _, c := range cands {
		st := sc.files[c.path]
		if st == nil || c.fi.Size() < st.offset {
			st = newFileState(c.path)
			sc.files[c.path] = st
		}
		if sc.conversations.val.Conversations[st.sess.ID].IsInternal {
			continue
		}
		if c.fi.Size() > st.offset {
			_ = st.read(c.path)
		}
		st.sess.ModTime, st.sess.Size = c.fi.ModTime(), c.fi.Size()
		if st.sess.Prompts == 0 && st.sess.Turns == 0 {
			continue
		}
		sc.describe(st)
		snap := st.sess.Snapshot()
		// agy records nothing when a command is cut off by the session
		// ending, so a call still open in an idle session is not running.
		if !snap.Active(now) {
			for _, e := range snap.Execs {
				if !e.Done() {
					e.End, e.Interrupted = snap.ModTime, true
				}
			}
		}
		out = append(out, snap)
	}
	return out, nil
}

func newFileState(path string) *fileState {
	logs := filepath.Dir(path)
	id := filepath.Base(filepath.Dir(filepath.Dir(logs))) // brain/<id>/.system_generated/logs
	return &fileState{
		sess: &claude.Session{
			Agent:      Agent,
			ID:         id,
			Path:       path,
			Entrypoint: "agy",
			Resume:     "agy --conversation " + id,
			ToolCounts: map[string]int{},
		},
		execs:   map[string]*claude.Exec{},
		tasks:   map[string]*claude.Task{},
		pending: map[int]*claude.Exec{},
		early:   map[int]*step{},
	}
}

// describe fills in what the transcript doesn't say. The title agy generates
// and the project directory live in separate, partial files.
func (sc *Scanner) describe(st *fileState) {
	s := st.sess
	meta := sc.conversations.val.Conversations[s.ID].Summary
	s.Title = annotationTitle(filepath.Join(sc.root, "annotations", s.ID+".pbtxt"))
	if s.Title == "" {
		s.Title = meta.Title
	}
	if s.Title == "" {
		s.Title = meta.Preview
	}
	s.Cwd = ""
	if len(meta.WorkspaceURIs) > 0 {
		s.Cwd = uriPath(meta.WorkspaceURIs[0])
	}
	if s.Cwd == "" {
		for dir, id := range sc.workspaces.val {
			if id == s.ID {
				s.Cwd = dir
			}
		}
	}
	if s.Cwd == "" {
		s.Cwd = st.cmdCwd
	}
}

var reAnnotationTitle = regexp.MustCompile(`(?m)^title:\s*("(?:[^"\\]|\\.)*")`)

func annotationTitle(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	m := reAnnotationTitle.FindSubmatch(data)
	if m == nil {
		return ""
	}
	if t, err := strconv.Unquote(string(m[1])); err == nil {
		return t
	}
	return strings.Trim(string(m[1]), `"`)
}

func uriPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p := u.Path
	if len(p) > 2 && p[0] == '/' && p[2] == ':' { // /C:/Users -> C:/Users
		p = p[1:]
	}
	return filepath.FromSlash(p)
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

type step struct {
	StepIndex       int        `json:"step_index"`
	Type            string     `json:"type"`
	Status          string     `json:"status"`
	Error           string     `json:"error"`
	ExitCode        *int       `json:"exit_code"`
	CreatedAt       string     `json:"created_at"`
	Content         string     `json:"content"`
	InputTokens     int64      `json:"input_tokens"`
	CacheReadTokens int64      `json:"cache_read_tokens"`
	OutputTokens    int64      `json:"output_tokens"`
	ToolCalls       []toolCall `json:"tool_calls"`
}

type toolCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

var (
	reCreated       = regexp.MustCompile(`(?m)^Created At: (\S+)`)
	reCompleted     = regexp.MustCompile(`(?m)^Completed At: (\S+)`)
	reExited        = regexp.MustCompile(`The command exited with code (-?\d+)`)
	reTaskStarted   = regexp.MustCompile(`background task with task id: (\S+)`)
	reTaskLog       = regexp.MustCompile(`Task logs are available at: (\S+)`)
	reCommandResult = regexp.MustCompile(`The command exited with code|background task with task id|Encountered error`)
	reTaskFinished  = regexp.MustCompile(`timestamp=(\S+) sender=\S+ priority=\S+ content=Task id "([^"]+)" finished with result:\s*The command exited with code (-?\d+)`)
	reModel         = regexp.MustCompile("`Model Selection` from .+? to (.+?)\\.(?:\\s|$)")
)

func (st *fileState) parse(ln []byte) {
	if len(ln) == 0 {
		return
	}
	var rec step
	if json.Unmarshal(ln, &rec) != nil {
		return
	}
	ts, _ := time.Parse(time.RFC3339Nano, rec.CreatedAt)
	st.touch(ts)

	switch rec.Type {
	case "USER_INPUT":
		st.stopPending(rec.StepIndex, ts)
		st.prompt(rec.Content)
	case "PLANNER_RESPONSE":
		st.stopPending(rec.StepIndex, ts)
		st.planner(&rec, ts)
	case "SYSTEM_MESSAGE":
		st.systemMessage(rec.Content, ts)
	default:
		if e := st.pending[rec.StepIndex]; e != nil {
			st.result(e, &rec, ts)
		} else if reCommandResult.MatchString(rec.Content) {
			st.early[rec.StepIndex] = &rec
		}
	}
}

func (st *fileState) touch(ts time.Time) {
	s := st.sess
	if ts.IsZero() {
		return
	}
	if s.StartedAt.IsZero() || ts.Before(s.StartedAt) {
		s.StartedAt = ts
	}
	if ts.After(s.LastActivity) {
		s.LastActivity = ts
	}
}

// stopPending closes commands from before step idx that still have no result:
// agy writes no result for a command that was interrupted. If one does turn
// up later, result corrects this.
func (st *fileState) stopPending(idx int, ts time.Time) {
	for at, e := range st.pending {
		if at < idx && !e.Done() {
			e.End, e.Interrupted = ts, true
		}
	}
}

func (st *fileState) prompt(content string) {
	s := st.sess
	if m := reModel.FindStringSubmatch(content); m != nil {
		s.Model = m[1]
	}
	text := content
	if _, rest, ok := strings.Cut(content, "<USER_REQUEST>"); ok {
		text, _, _ = strings.Cut(rest, "</USER_REQUEST>")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.Prompts++
	s.LastPrompt = claude.OneLine(text, 200)
	if s.FirstAsk == "" {
		s.FirstAsk = claude.OneLine(text, 120)
	}
}

// planner records a model turn and its tool calls. Results carry no call id:
// the result of a turn's nth call (from 0) is step turn+1+n.
func (st *fileState) planner(rec *step, ts time.Time) {
	s := st.sess
	s.Turns++
	s.InputTokens += rec.InputTokens + rec.CacheReadTokens
	s.OutputTokens += rec.OutputTokens
	for i, call := range rec.ToolCalls {
		s.ToolCounts[call.Name]++
		if call.Name != "run_command" {
			continue
		}
		var in struct {
			CommandLine string
			Cwd         string
			ToolAction  string `json:"toolAction"`
			ToolSummary string `json:"toolSummary"`
		}
		if json.Unmarshal(call.Args, &in) != nil || in.CommandLine == "" {
			continue
		}
		at := rec.StepIndex + 1 + i
		id := "step-" + strconv.Itoa(at)
		if _, dup := st.execs[id]; dup {
			continue
		}
		desc := in.ToolSummary
		if desc == "" {
			desc = in.ToolAction
		}
		e := &claude.Exec{ToolUseID: id, Tool: call.Name, Command: in.CommandLine, Description: desc, Start: ts}
		st.execs[id] = e
		s.Execs = append(s.Execs, e)
		if st.cmdCwd == "" {
			st.cmdCwd = in.Cwd
		}
		if r := st.early[at]; r != nil {
			delete(st.early, at)
			rts, _ := time.Parse(time.RFC3339Nano, r.CreatedAt)
			st.result(e, r, rts)
		} else {
			st.pending[at] = e
		}
	}
}

func (st *fileState) result(e *claude.Exec, rec *step, ts time.Time) {
	delete(st.pending, rec.StepIndex)
	e.Interrupted = false
	if t := stamp(reCreated, rec.Content); !t.IsZero() {
		e.Start = t
	}
	e.End = ts
	if t := stamp(reCompleted, rec.Content); !t.IsZero() {
		e.End = t
		st.touch(t)
	}

	if m := reTaskStarted.FindStringSubmatch(rec.Content); m != nil {
		_, id := cutLast(m[1], "/")
		t := &claude.Task{ID: id, ToolUseID: e.ToolUseID, Command: e.Command, Description: e.Description, Start: e.Start}
		if lm := reTaskLog.FindStringSubmatch(rec.Content); lm != nil {
			t.OutputFile = uriPath(lm[1])
		}
		if st.tasks[id] == nil {
			st.tasks[id] = t
			st.sess.Tasks = append(st.sess.Tasks, t)
		}
		e.Background, e.TaskID, e.Output = true, id, "Running in the background as "+id
		return
	}

	code := rec.ExitCode
	if code == nil {
		if m := reExited.FindStringSubmatch(rec.Content); m != nil {
			if c, err := strconv.Atoi(m[1]); err == nil {
				code = &c
			}
		}
	}
	switch {
	case code != nil && *code != 0:
		e.IsError = true
		e.Output = "Exit code " + strconv.Itoa(*code) + "\n\n" + commandOutput(rec.Content)
	case code != nil:
		e.Output = commandOutput(rec.Content)
	case rec.Error != "":
		e.IsError, e.Output = true, rec.Error
	default:
		e.IsError, e.Output = rec.Status == "ERROR", commandOutput(rec.Content)
	}
	e.Output = claude.Clip(e.Output, 8000)
}

func stamp(re *regexp.Regexp, content string) time.Time {
	m := re.FindStringSubmatch(content)
	if m == nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, m[1])
	return t
}

func cutLast(s, sep string) (string, string) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):]
	}
	return "", s
}

// commandOutput strips agy's framing from a result, leaving what the command
// printed.
func commandOutput(content string) string {
	var out []string
	for _, l := range strings.Split(content, "\n") {
		l = strings.TrimRight(strings.TrimPrefix(l, "\t\t\t\t"), "\r")
		switch t := strings.TrimSpace(l); {
		case len(out) == 0 && t == "":
		case strings.HasPrefix(t, "Created At: "), strings.HasPrefix(t, "Completed At: "):
		case strings.HasPrefix(t, "The command exited with code "):
		case t == "Output:", t == "Stdout:", t == "Stderr:":
		default:
			out = append(out, l)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// systemMessage picks up the notices agy delivers when background tasks end.
func (st *fileState) systemMessage(content string, ts time.Time) {
	for _, m := range reTaskFinished.FindAllStringSubmatch(content, -1) {
		_, id := cutLast(m[2], "/")
		t := st.tasks[id]
		if t == nil {
			continue
		}
		code, err := strconv.Atoi(m[3])
		if err != nil {
			continue
		}
		t.Status, t.ExitCode, t.End = "completed", &code, ts
		if at, err := time.Parse(time.RFC3339Nano, m[1]); err == nil {
			t.End = at
		}
	}
	if strings.Contains(content, "background tasks have been stopped") {
		for _, t := range st.sess.Tasks {
			if t.Status == "" {
				t.Status, t.End = "killed", ts
			}
		}
	}
}
