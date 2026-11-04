// Package opencode discovers OpenCode sessions by reading the SQLite database
// OpenCode keeps under ~/.local/share/opencode. It is read-only: it never
// writes to OpenCode's files.
package opencode

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"agentbox/internal/claude"
)

// Agent is the name recorded on runs and sessions that came from OpenCode.
const Agent = "opencode"

// DataDir is where OpenCode keeps its state. OPENCODE_DB can still point the
// database somewhere else.
func DataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "opencode")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share", "opencode")
}

// DBPath is OpenCode's session database. Release channels keep theirs under a
// channel-specific name, so the newest one wins when the default is missing.
func DBPath() string {
	dir := DataDir()
	if p := os.Getenv("OPENCODE_DB"); p != "" {
		if p == ":memory:" {
			return ""
		}
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(dir, p)
	}
	if c := os.Getenv("OPENCODE_TUI_CHANNEL"); c != "" && os.Getenv("OPENCODE_DISABLE_CHANNEL_DB") != "true" {
		if p := filepath.Join(dir, "opencode-"+c+".db"); exists(p) {
			return p
		}
	}
	if p := filepath.Join(dir, "opencode.db"); exists(p) {
		return p
	}
	if paths, _ := filepath.Glob(filepath.Join(dir, "opencode-*.db")); len(paths) > 0 {
		sort.Slice(paths, func(i, j int) bool { return modTime(paths[i]).After(modTime(paths[j])) })
		return paths[0]
	}
	return filepath.Join(dir, "opencode.db")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func modTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

var errNoDB = errors.New("opencode: no database")

// Scanner reads OpenCode's session database incrementally. Sessions and
// messages are random access rows rather than an append-only file, so it
// keeps what it parsed and re-reads only the messages that changed.
type Scanner struct {
	mu          sync.Mutex
	path        string
	auto        bool // path came from DBPath, so it may appear or move later
	db          *sql.DB
	states      map[string]*sessionState
	dataVersion int64 // SQLite's change counter for other connections
}

func NewScanner() *Scanner {
	return &Scanner{path: DBPath(), auto: true, states: map[string]*sessionState{}}
}

// Close releases the database file. A scanner can still be used afterwards:
// the next Scan opens it again.
func (sc *Scanner) Close() error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.db == nil {
		return nil
	}
	err := sc.db.Close()
	sc.db = nil
	return err
}

// Scan returns sessions whose database row changed within maxAge, most
// recently active first, at most limit of them.
func (sc *Scanner) Scan(maxAge time.Duration, limit int) ([]*claude.Session, error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// OpenCode may not have created its database yet, or a release channel
	// may have started using a different one.
	if sc.auto && !exists(sc.path) {
		if p := DBPath(); p != sc.path {
			if sc.db != nil {
				sc.db.Close()
				sc.db = nil
			}
			sc.path = p
		}
	}
	db, err := sc.connect()
	if err != nil {
		if errors.Is(err, errNoDB) {
			return nil, nil
		}
		return nil, err
	}

	// Another connection (OpenCode itself) can rewrite a message without
	// touching its session row, so watch the database's change counter too.
	var dataVersion int64
	if err := db.QueryRow("pragma data_version").Scan(&dataVersion); err != nil {
		return nil, err
	}
	dataChanged := sc.dataVersion != 0 && dataVersion != sc.dataVersion
	sc.dataVersion = dataVersion

	cutoff := time.Now().Add(-maxAge).UnixMilli()
	rows, err := db.Query(sessionQuery, cutoff)
	if err != nil {
		if missingSchema(err) {
			return nil, nil
		}
		return nil, err
	}
	var listed []*sessionRow
	for rows.Next() {
		r := &sessionRow{}
		if err := rows.Scan(&r.id, &r.project, &r.title, &r.directory, &r.version, &r.model,
			&r.cost, &r.input, &r.output, &r.cacheRead, &r.cacheWrite, &r.created, &r.updated); err != nil {
			rows.Close()
			return nil, err
		}
		listed = append(listed, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	now := time.Now()
	var out []*claude.Session
	live := make(map[string]bool, len(listed))
	for _, r := range listed {
		live[r.id] = true
		st := sc.states[r.id]
		if st == nil {
			st = newState(sc.path, r)
			sc.states[r.id] = st
		} else {
			st.update(r)
		}
		if err := st.refresh(db, dataChanged); err != nil {
			return nil, err
		}
		snap := st.sess.Snapshot()
		// OpenCode writes no result for a command cut off by the session
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
	for id := range sc.states {
		if !live[id] {
			delete(sc.states, id)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (sc *Scanner) connect() (*sql.DB, error) {
	if sc.db != nil {
		return sc.db, nil
	}
	if sc.path == "" || !exists(sc.path) {
		return nil, errNoDB
	}
	u := url.URL{Scheme: "file", Path: absoluteURLPath(sc.path)}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("_pragma", "busy_timeout(2000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	// One connection keeps pragma data_version meaningful: it is tracked per
	// connection, and every caller already holds the scanner's lock.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	sc.db = db
	return db, nil
}

// absoluteURLPath gives a SQLite file: URI the leading slash that keeps a
// Windows drive letter out of the authority component.
func absoluteURLPath(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

const sessionQuery = `
select id, coalesce(project_id, ''), coalesce(title, ''), coalesce(directory, ''),
       coalesce(version, ''), coalesce(model, ''), coalesce(cost, 0),
       coalesce(tokens_input, 0), coalesce(tokens_output, 0),
       coalesce(tokens_cache_read, 0), coalesce(tokens_cache_write, 0),
       coalesce(time_created, 0), time_updated
from session_v2
where time_updated >= ? and parent_id is null and time_archived is null
order by time_updated desc`

const messageIndexQuery = `select id, seq, time_updated from session_message where session_id = ?`

const messageQuery = `select id, type, seq, time_updated, data from session_message where id in (%s)`

type sessionRow struct {
	id, project, title, directory, version, model string
	cost                                          float64
	input, output, cacheRead, cacheWrite          int64
	created, updated                              int64
}

type sessionState struct {
	sess       *claude.Session
	msgs       map[string]*message
	rowUpdated int64
	scanned    int64
}

func newState(path string, r *sessionRow) *sessionState {
	st := &sessionState{
		sess: &claude.Session{
			Agent:      Agent,
			ID:         r.id,
			Path:       path,
			Project:    r.project,
			Entrypoint: "opencode",
			Resume:     "opencode --session " + r.id,
			ToolCounts: map[string]int{},
		},
		msgs:    map[string]*message{},
		scanned: -1,
	}
	st.update(r)
	return st
}

// update copies what the session row knows. Message-derived fields are rebuilt
// by refresh.
func (st *sessionState) update(r *sessionRow) {
	s := st.sess
	st.rowUpdated = r.updated
	s.Title = r.title
	s.Cwd = filepath.FromSlash(r.directory)
	s.Version = r.version
	s.Model = modelName(r.model)
	s.StartedAt = unixMilli(r.created)
	s.ModTime = unixMilli(r.updated)
	if s.LastActivity.IsZero() {
		s.LastActivity = s.ModTime
	}
	s.CostUSD = r.cost
	s.InputTokens = r.input + r.cacheRead + r.cacheWrite
	s.OutputTokens = r.output
}

func (st *sessionState) refresh(db *sql.DB, force bool) error {
	if st.scanned == st.rowUpdated && !force {
		return nil
	}
	rows, err := db.Query(messageIndexQuery, st.sess.ID)
	if err != nil {
		return err
	}
	present := make(map[string]bool, len(st.msgs))
	var changed []string
	for rows.Next() {
		var id string
		var seq, updated int64
		if err := rows.Scan(&id, &seq, &updated); err != nil {
			rows.Close()
			return err
		}
		present[id] = true
		if m := st.msgs[id]; m == nil || m.updated != updated {
			changed = append(changed, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	// Messages can be removed by compaction or undo.
	for id := range st.msgs {
		if !present[id] {
			delete(st.msgs, id)
		}
	}
	for start := 0; start < len(changed); start += 200 {
		end := min(start+200, len(changed))
		if err := st.read(db, changed[start:end]); err != nil {
			return err
		}
	}
	st.rebuild()
	st.scanned = st.rowUpdated
	return nil
}

func (st *sessionState) read(db *sql.DB, ids []string) error {
	args := make([]any, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		args[i], marks[i] = id, "?"
	}
	rows, err := db.Query(fmt.Sprintf(messageQuery, strings.Join(marks, ",")), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, typ string
		var seq, updated int64
		var data []byte
		if err := rows.Scan(&id, &typ, &seq, &updated, &data); err != nil {
			return err
		}
		st.msgs[id] = parseMessage(typ, seq, updated, data)
	}
	return rows.Err()
}

// rebuild recomputes everything derived from messages, so a message that is
// rewritten while it streams is never counted twice.
func (st *sessionState) rebuild() {
	s := st.sess
	s.Prompts, s.Turns = 0, 0
	s.FirstAsk, s.LastPrompt = "", ""
	s.ToolCounts = map[string]int{}
	s.Execs = nil
	s.Tasks = nil

	ordered := make([]*message, 0, len(st.msgs))
	for _, m := range st.msgs {
		ordered = append(ordered, m)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].seq < ordered[j].seq })

	tasks := map[string]*claude.Task{}
	last := time.Time{}
	for _, m := range ordered {
		if m.at.After(last) {
			last = m.at
		}
		if m.prompt != "" {
			s.Prompts++
			if s.FirstAsk == "" {
				s.FirstAsk = claude.OneLine(m.prompt, 120)
			}
			s.LastPrompt = claude.OneLine(m.prompt, 200)
		}
		if m.turn {
			s.Turns++
		}
		for name, n := range m.tools {
			s.ToolCounts[name] += n
		}
		for _, ep := range m.execs {
			s.Execs = append(s.Execs, ep.exec)
			if ep.shellID == "" {
				continue
			}
			t := tasks[ep.shellID]
			if t == nil {
				t = &claude.Task{ID: ep.shellID}
				tasks[ep.shellID] = t
				s.Tasks = append(s.Tasks, t)
			}
			t.ToolUseID = ep.exec.ToolUseID
			t.Command = ep.exec.Command
			if t.Description == "" {
				t.Description = ep.exec.Description
			}
			if t.Start.IsZero() || ep.exec.Start.Before(t.Start) {
				t.Start = ep.exec.Start
			}
			if t.OutputFile == "" {
				t.OutputFile = ep.outputFile
			}
			if ep.killed && t.Status == "" && t.End.IsZero() {
				t.Status, t.End = "killed", ep.exec.End
			}
			ep.exec.Background, ep.exec.TaskID = true, ep.shellID
		}
		for _, n := range m.notices {
			t := tasks[n.shellID]
			if t == nil {
				t = &claude.Task{ID: n.shellID, Start: n.at}
				tasks[n.shellID] = t
				s.Tasks = append(s.Tasks, t)
			}
			if n.state != "" {
				t.Status = n.state
			}
			if n.exit != nil {
				t.ExitCode = n.exit
			}
			if t.End.IsZero() || n.at.After(t.End) {
				t.End = n.at
			}
			t.Summary = n.summary()
		}
	}
	if last.After(s.LastActivity) {
		s.LastActivity = last
	}
}

type message struct {
	seq, updated int64
	at           time.Time
	prompt       string
	turn         bool
	tools        map[string]int
	execs        []execPart
	notices      []*notice
}

type execPart struct {
	exec       *claude.Exec
	shellID    string
	outputFile string
	killed     bool
}

type notice struct {
	shellID string
	state   string
	exit    *int
	at      time.Time
}

func (n *notice) summary() string {
	s := "Background shell"
	if n.state != "" {
		s += " " + n.state
	}
	if n.exit != nil {
		s += " (exit " + strconv.Itoa(*n.exit) + ")"
	}
	return s
}

type part struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	ID    string `json:"id"`
	State *struct {
		Status string `json:"status"`
		Input  struct {
			Command     string `json:"command"`
			Description string `json:"description"`
			Background  bool   `json:"background"`
		} `json:"input"`
		Content  []textPart `json:"content"`
		Metadata struct {
			ShellID string `json:"shellID"`
			Exit    *int   `json:"exit"`
		} `json:"metadata"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"state"`
	Time struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
}

func parseMessage(typ string, seq, updated int64, raw []byte) *message {
	m := &message{seq: seq, updated: updated, tools: map[string]int{}}
	switch typ {
	case "user":
		var d struct {
			Time struct {
				Created int64 `json:"created"`
			} `json:"time"`
			Text     string `json:"text"`
			Metadata struct {
				DisplayText string `json:"displayText"`
			} `json:"metadata"`
		}
		if json.Unmarshal(raw, &d) != nil {
			return m
		}
		m.at = unixMilli(d.Time.Created)
		m.prompt = strings.TrimSpace(d.Text)
		if m.prompt == "" {
			m.prompt = strings.TrimSpace(d.Metadata.DisplayText)
		}
	case "assistant":
		var d struct {
			Time struct {
				Created int64 `json:"created"`
			} `json:"time"`
			Content []part `json:"content"`
		}
		if json.Unmarshal(raw, &d) != nil {
			return m
		}
		m.at, m.turn = unixMilli(d.Time.Created), true
		for i := range d.Content {
			m.addTool(&d.Content[i])
		}
	case "synthetic":
		var d struct {
			Time struct {
				Created int64 `json:"created"`
			} `json:"time"`
			Text     string `json:"text"`
			Metadata struct {
				Source  string `json:"source"`
				ShellID string `json:"shellID"`
				State   string `json:"state"`
				Exit    *int   `json:"exit"`
			} `json:"metadata"`
		}
		if json.Unmarshal(raw, &d) != nil {
			return m
		}
		m.at = unixMilli(d.Time.Created)
		if d.Metadata.ShellID != "" && (d.Metadata.Source == "" || d.Metadata.Source == "shell") {
			m.notices = append(m.notices, &notice{
				shellID: d.Metadata.ShellID,
				state:   taskState(d.Metadata.State),
				exit:    d.Metadata.Exit,
				at:      m.at,
			})
		}
	}
	return m
}

var reOutputFile = regexp.MustCompile(`(?m)Output is streaming to: (.+?)\r?$`)

type textPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (m *message) addTool(p *part) {
	if p.Type != "tool" || p.Name == "" {
		return
	}
	m.tools[p.Name]++
	if p.Name != "shell" && p.Name != "bash" {
		return
	}
	st := p.State
	if st == nil || st.Input.Command == "" {
		return
	}
	start := unixMilli(p.Time.Created)
	if start.IsZero() {
		start = m.at
	}
	e := &claude.Exec{
		ToolUseID:   p.ID,
		Tool:        p.Name,
		Command:     st.Input.Command,
		Description: st.Input.Description,
		Start:       start,
	}
	if st.Status != "running" {
		e.End = unixMilli(p.Time.Completed)
		if e.End.IsZero() {
			e.End = m.at
		}
	}
	if out := textOf(st.Content); out != "" {
		e.Output = out
	}
	if st.Error != nil && st.Error.Message != "" {
		e.Output, e.IsError = st.Error.Message, true
		if st.Error.Type == "aborted" {
			e.Interrupted, e.IsError = true, false
		}
	}
	if st.Metadata.Exit != nil && *st.Metadata.Exit != 0 {
		e.IsError = true
		e.Output = "Exit code " + strconv.Itoa(*st.Metadata.Exit) + "\n\n" + e.Output
	}
	e.Output = claude.Clip(e.Output, 8000)

	shellID := st.Metadata.ShellID
	outputFile := ""
	if shellID != "" {
		if mm := reOutputFile.FindStringSubmatch(e.Output); mm != nil {
			outputFile = strings.TrimSpace(mm[1])
		}
	}
	e.Background = st.Input.Background || shellID != ""

	m.execs = append(m.execs, execPart{
		exec:       e,
		shellID:    shellID,
		outputFile: outputFile,
		killed:     st.Error != nil && st.Error.Type == "aborted",
	})
}

func textOf(parts []textPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Type != "text" || p.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p.Text)
	}
	return b.String()
}

func taskState(state string) string {
	switch state {
	case "running":
		return ""
	case "completed", "failed", "killed":
		return state
	case "error":
		return "failed"
	case "cancelled", "canceled", "stopped":
		return "killed"
	}
	return state
}

func modelName(raw string) string {
	var m struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
	}
	if json.Unmarshal([]byte(raw), &m) == nil {
		return m.ID
	}
	return strings.TrimSpace(raw)
}

func unixMilli(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func missingSchema(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such table")
}
