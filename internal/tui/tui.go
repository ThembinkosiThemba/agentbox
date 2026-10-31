// Package tui is agentbox's dashboard: agentbox runs on one tab, Claude Code
// sessions (with their commands and background tasks) on the other.
package tui

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"agentbox/internal/claude"
	"agentbox/internal/store"
	"agentbox/internal/supervisor"
)

type Hooks struct {
	Restart func(*store.Store, *store.Run) (*store.Run, error)
	Version string
}

func Run(st *store.Store, hooks Hooks) error {
	p := tea.NewProgram(newModel(st, hooks), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

type tab int

const (
	tabRuns tab = iota
	tabClaude
)

type screen int

const (
	screenMain screen = iota
	screenSession
	screenLog
)

type overlay int

const (
	ovNone overlay = iota
	ovHelp
	ovConfirm
	ovInput
)

const (
	frameEvery   = 120 * time.Millisecond
	refreshEvery = time.Second
	sessionAge   = 7 * 24 * time.Hour
	sessionLimit = 80
)

type model struct {
	st      *store.Store
	scanner *claude.Scanner
	hooks   Hooks
	cwd     string

	w, h    int
	now     time.Time
	frame   int
	tab     tab
	screen  screen
	overlay overlay
	loading bool
	loaded  bool

	runs     []*store.Run
	sessions []*claude.Session
	live     int

	runSel, runOff int
	runID          string
	sesSel, sesOff int
	sesID          string
	detSel, detOff int
	detKey         string
	detSession     *claude.Session

	filter string

	preview previewState
	log     logState

	input        textinput.Model
	inputPurpose string
	confirmText  string
	confirmDo    func() tea.Cmd

	tabHits [2][2]int // header x-ranges of the tab labels, for clicks

	flash      string
	flashErr   bool
	flashUntil time.Time
}

type previewState struct {
	path      string
	lines     []string
	truncated bool
}

type logState struct {
	title     string
	path      string
	runID     string
	sessionID string
	taskID    string
	lines     []string
	truncated bool
	off       int
	follow    bool
	wrap      bool
	back      screen
}

type (
	frameMsg   struct{}
	refreshMsg struct{}
	dataMsg    struct {
		runs     []*store.Run
		sessions []*claude.Session
		live     int
		tick     bool
	}
	actionMsg struct {
		text string
		err  error
	}
)

func newModel(st *store.Store, hooks Hooks) *model {
	cwd, _ := os.Getwd()
	in := textinput.New()
	in.Prompt = "› "
	in.PromptStyle = sAccentB
	in.TextStyle = sText
	in.PlaceholderStyle = sFaint
	in.Cursor.Style = sAccent
	return &model{st: st, scanner: claude.NewScanner(), hooks: hooks, cwd: cwd, now: time.Now(), input: in}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.load(true), frameTick())
}

func frameTick() tea.Cmd {
	return tea.Tick(frameEvery, func(time.Time) tea.Msg { return frameMsg{} })
}

func (m *model) load(tick bool) tea.Cmd {
	m.loading = true
	st, sc := m.st, m.scanner
	return func() tea.Msg {
		runs, _ := st.List()
		sessions, _ := sc.Scan(sessionAge, sessionLimit)
		return dataMsg{runs: runs, sessions: sessions, live: claude.LiveProcesses(), tick: tick}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.Width = min(64, m.w-12)
		return m, nil

	case frameMsg:
		m.frame++
		m.now = time.Now()
		return m, frameTick()

	case refreshMsg:
		if m.loading {
			return m, nil
		}
		return m, m.load(true)

	case dataMsg:
		m.loading, m.loaded = false, true
		m.runs, m.sessions, m.live = msg.runs, msg.sessions, msg.live
		m.reselect()
		m.loadPreview()
		m.loadLog()
		if msg.tick {
			return m, tea.Tick(refreshEvery, func(time.Time) tea.Msg { return refreshMsg{} })
		}
		return m, nil

	case actionMsg:
		if msg.err != nil {
			m.setFlash(msg.err.Error(), true)
		} else if msg.text != "" {
			m.setFlash(msg.text, false)
		}
		if !m.loading {
			return m, m.load(false)
		}
		return m, nil

	case tea.MouseMsg:
		if m.overlay != ovNone {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scroll(-3)
		case tea.MouseButtonWheelDown:
			m.scroll(3)
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionPress {
				m.click(msg.X, msg.Y)
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *model) setFlash(s string, isErr bool) {
	m.flash, m.flashErr, m.flashUntil = s, isErr, time.Now().Add(4*time.Second)
}

// ---- keys ----

func (m *model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.overlay {
	case ovHelp:
		m.overlay = ovNone
		return m, nil
	case ovConfirm:
		switch k {
		case "y", "Y", "enter":
			m.overlay = ovNone
			if m.confirmDo != nil {
				return m, m.confirmDo()
			}
		case "n", "N", "esc", "q":
			m.overlay = ovNone
		}
		return m, nil
	case ovInput:
		return m.keyInput(msg)
	}

	switch k {
	case "?":
		m.overlay = ovHelp
		return m, nil
	case "q":
		if m.screen == screenMain {
			return m, tea.Quit
		}
		m.back()
		return m, nil
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.reselect()
			return m, nil
		}
		m.back()
		return m, nil
	}

	if m.screen == screenLog {
		return m.keyLog(k)
	}

	switch k {
	case "tab", "shift+tab":
		if m.screen == screenMain {
			m.switchTab(1 - m.tab)
		}
	case "1":
		m.switchTab(tabRuns)
	case "2":
		m.switchTab(tabClaude)
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+u":
		m.move(-10)
	case "pgdown", "ctrl+d":
		m.move(10)
	case "home", "g":
		m.move(-1 << 30)
	case "end", "G":
		m.move(1 << 30)
	case "/":
		m.openInput("filter", "filter…", m.filter)
	case "enter":
		m.open()
	case "right", "l":
		if m.screen == screenMain {
			m.switchTab(tabClaude)
		} else {
			m.open()
		}
	case "left", "h":
		if m.screen == screenMain {
			m.switchTab(tabRuns)
		} else {
			m.back()
		}
	}

	if m.screen == screenMain && m.tab == tabRuns {
		r := m.selectedRun()
		switch k {
		case "n":
			m.openInput("new", "command to run, e.g. npm run dev", "")
		case "x":
			if r != nil {
				return m, m.confirmKill(r)
			}
		case "r":
			if r != nil {
				return m, m.confirmRestart(r)
			}
		case "d":
			if r != nil {
				return m, m.confirmDelete(r)
			}
		}
	}
	return m, nil
}

func (m *model) keyLog(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "up", "k":
		m.scroll(-1)
	case "down", "j":
		m.scroll(1)
	case "pgup", "ctrl+u", "b":
		m.scroll(-(m.h - 4))
	case "pgdown", "ctrl+d", " ":
		m.scroll(m.h - 4)
	case "home", "g":
		m.log.follow, m.log.off = false, 0
	case "end", "G":
		m.log.follow = true
	case "f":
		m.log.follow = !m.log.follow
	case "w":
		m.log.wrap = !m.log.wrap
	case "x", "r":
		if m.log.runID == "" {
			return m, nil
		}
		r := m.runByID(m.log.runID)
		if r == nil {
			return m, nil
		}
		if k == "x" {
			return m, m.confirmKill(r)
		}
		return m, m.confirmRestart(r)
	}
	return m, nil
}

func (m *model) keyInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.inputPurpose == "filter" {
			m.filter = ""
			m.reselect()
		}
		m.overlay = ovNone
		m.input.Blur()
		return m, nil
	case "enter":
		m.overlay = ovNone
		m.input.Blur()
		val := strings.TrimSpace(m.input.Value())
		if m.inputPurpose == "new" && val != "" {
			return m, m.startRun(val)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.inputPurpose == "filter" {
		m.filter = m.input.Value()
		m.reselect()
	}
	return m, cmd
}

func (m *model) openInput(purpose, placeholder, value string) {
	m.inputPurpose = purpose
	m.input.Placeholder = placeholder
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.input.Focus()
	m.overlay = ovInput
}

func (m *model) switchTab(t tab) {
	if m.screen != screenMain || m.tab == t {
		return
	}
	m.tab, m.filter = t, ""
	m.reselect()
	m.loadPreview()
}

// open drills into the selection.
func (m *model) open() {
	switch {
	case m.screen == screenMain && m.tab == tabRuns:
		if r := m.selectedRun(); r != nil {
			m.openLog(logState{title: r.DisplayName(), path: m.st.LogPath(r.ID), runID: r.ID, back: screenMain})
		}
	case m.screen == screenMain && m.tab == tabClaude:
		if s := m.selectedSession(); s != nil {
			m.detSession, m.detSel, m.detOff, m.detKey, m.filter = s, 0, 0, "", ""
			m.screen = screenSession
			m.reselect()
			m.loadPreview()
		}
	case m.screen == screenSession:
		items := m.detailItems()
		if m.detSel < len(items) {
			if t := items[m.detSel].task; t != nil && t.OutputFile != "" {
				m.openLog(logState{title: taskTitle(t), path: t.OutputFile, sessionID: m.detSession.ID, taskID: t.ID, back: screenSession})
			}
		}
	}
}

func (m *model) openLog(l logState) {
	l.follow, l.wrap = true, true
	m.log = l
	m.screen = screenLog
	m.loadLog()
}

func (m *model) back() {
	switch m.screen {
	case screenLog:
		m.screen = m.log.back
	case screenSession:
		m.screen, m.filter = screenMain, ""
		m.reselect()
	}
	m.loadPreview()
}

// click handles a left click: tab labels in the header switch tabs, list
// rows select, and clicking the selected row opens it.
func (m *model) click(x, y int) {
	if y == 0 {
		for t, r := range m.tabHits {
			if x >= r[0] && x < r[1] {
				if m.screen != screenMain {
					m.screen = screenMain
					m.filter = ""
				}
				m.switchTab(tab(t))
				m.loadPreview()
			}
		}
		return
	}
	if m.screen == screenLog {
		return
	}
	horiz, lw, lh, _, _ := m.split(m.w, m.h-2)
	if (horiz && x >= lw) || y < 2 || y >= lh {
		return
	}
	sel, off := &m.runSel, m.runOff
	switch {
	case m.screen == screenSession:
		sel, off = &m.detSel, m.detOff
	case m.tab == tabClaude:
		sel, off = &m.sesSel, m.sesOff
	}
	idx := off + (y-2)/2
	if idx == *sel {
		m.open()
		return
	}
	m.move(idx - *sel)
}

func (m *model) move(d int) {
	switch {
	case m.screen == screenSession:
		m.detSel = clamp(m.detSel+d, 0, len(m.detailItems())-1)
		if items := m.detailItems(); m.detSel < len(items) {
			m.detKey = items[m.detSel].key()
		}
	case m.tab == tabRuns:
		runs := m.visibleRuns()
		m.runSel = clamp(m.runSel+d, 0, len(runs)-1)
		if m.runSel < len(runs) {
			m.runID = runs[m.runSel].ID
		}
	default:
		ss := m.visibleSessions()
		m.sesSel = clamp(m.sesSel+d, 0, len(ss)-1)
		if m.sesSel < len(ss) {
			m.sesID = ss[m.sesSel].ID
		}
	}
	m.loadPreview()
}

func (m *model) scroll(d int) {
	if m.screen != screenLog {
		if d < 0 {
			m.move(-1)
		} else {
			m.move(1)
		}
		return
	}
	m.log.follow = false
	m.log.off = max(m.log.off+d, 0)
}

// reselect keeps the cursor on the same item across refreshes and filters.
func (m *model) reselect() {
	runs := m.visibleRuns()
	m.runSel = clamp(m.runSel, 0, len(runs)-1)
	for i, r := range runs {
		if r.ID == m.runID {
			m.runSel = i
		}
	}
	if m.runSel < len(runs) {
		m.runID = runs[m.runSel].ID
	}

	ss := m.visibleSessions()
	m.sesSel = clamp(m.sesSel, 0, len(ss)-1)
	for i, s := range ss {
		if s.ID == m.sesID {
			m.sesSel = i
		}
	}
	if m.sesSel < len(ss) {
		m.sesID = ss[m.sesSel].ID
	}

	if m.detSession != nil {
		for _, s := range m.sessions {
			if s.ID == m.detSession.ID {
				m.detSession = s
			}
		}
		items := m.detailItems()
		m.detSel = clamp(m.detSel, 0, len(items)-1)
		for i, it := range items {
			if it.key() == m.detKey {
				m.detSel = i
			}
		}
		if m.detSel < len(items) {
			m.detKey = items[m.detSel].key()
		}
	}
}

// ---- data views ----

func (m *model) visibleRuns() []*store.Run {
	out := make([]*store.Run, 0, len(m.runs))
	for _, r := range m.runs {
		if matches(m.filter, r.ID, r.Name, r.CommandLine(), r.Cwd, r.Agent, string(r.Status)) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := !out[i].Status.Terminal(), !out[j].Status.Terminal()
		if ai != aj {
			return ai
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (m *model) visibleSessions() []*claude.Session {
	out := make([]*claude.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if matches(m.filter, s.ID, s.DisplayTitle(), s.Cwd, s.Branch, s.LastPrompt) {
			out = append(out, s)
		}
	}
	now := m.now
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i].Active(now), out[j].Active(now)
		if ai != aj {
			return ai
		}
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out
}

type detItem struct {
	task *claude.Task
	exec *claude.Exec
}

func (d detItem) key() string {
	if d.task != nil {
		return "t:" + d.task.ID
	}
	return "e:" + d.exec.ToolUseID
}

// detailItems lists a session's background tasks, then its other commands,
// newest first.
func (m *model) detailItems() []detItem {
	s := m.detSession
	if s == nil {
		return nil
	}
	var out []detItem
	for i := len(s.Tasks) - 1; i >= 0; i-- {
		t := s.Tasks[i]
		if matches(m.filter, t.ID, t.Command, t.Description, t.Summary) {
			out = append(out, detItem{task: t})
		}
	}
	for i := len(s.Execs) - 1; i >= 0; i-- {
		e := s.Execs[i]
		if e.TaskID != "" {
			continue // shown as its task
		}
		if matches(m.filter, e.Command, e.Description, e.Output) {
			out = append(out, detItem{exec: e})
		}
	}
	return out
}

func (m *model) selectedRun() *store.Run {
	if m.tab != tabRuns {
		return nil
	}
	runs := m.visibleRuns()
	if m.runSel < len(runs) {
		return runs[m.runSel]
	}
	return nil
}

func (m *model) selectedSession() *claude.Session {
	ss := m.visibleSessions()
	if m.sesSel < len(ss) {
		return ss[m.sesSel]
	}
	return nil
}

func (m *model) runByID(id string) *store.Run {
	for _, r := range m.runs {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func (m *model) taskByID(sessionID, taskID string) (*claude.Session, *claude.Task) {
	for _, s := range m.sessions {
		if s.ID != sessionID {
			continue
		}
		for _, t := range s.Tasks {
			if t.ID == taskID {
				return s, t
			}
		}
	}
	return nil, nil
}

// loadPreview reads the output shown beside the selection.
func (m *model) loadPreview() {
	path := ""
	switch {
	case m.screen == screenMain && m.tab == tabRuns:
		if r := m.selectedRun(); r != nil {
			path = m.st.LogPath(r.ID)
		}
	case m.screen == screenSession:
		if items := m.detailItems(); m.detSel < len(items) && items[m.detSel].task != nil {
			path = items[m.detSel].task.OutputFile
		}
	}
	m.preview = previewState{path: path}
	if path == "" {
		return
	}
	lines, truncated, err := store.Tail(path, 400, 256<<10)
	if err != nil {
		return
	}
	for i := range lines {
		lines[i] = sanitize(lines[i])
	}
	m.preview.lines, m.preview.truncated = lines, truncated
}

func (m *model) loadLog() {
	if m.screen != screenLog || m.log.path == "" {
		return
	}
	lines, truncated, err := store.Tail(m.log.path, 20000, 4<<20)
	if err != nil {
		m.log.lines = nil
		return
	}
	for i := range lines {
		lines[i] = sanitize(lines[i])
	}
	m.log.lines, m.log.truncated = lines, truncated
}

// ---- actions ----

func (m *model) confirmKill(r *store.Run) tea.Cmd {
	if r.Status.Terminal() && r.Status != store.StatusLost {
		m.setFlash(r.ID+" is not running", true)
		return nil
	}
	m.ask(fmt.Sprintf("Stop %s and everything it started?", runLabel(r)), func() tea.Cmd {
		m.setFlash("stopping "+r.ID+"…", false)
		st := m.st
		return func() tea.Msg {
			cur, err := supervisor.Kill(st, r)
			if err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{text: cur.ID + " " + string(cur.Status)}
		}
	})
	return nil
}

func (m *model) confirmRestart(r *store.Run) tea.Cmd {
	do := func() tea.Cmd {
		m.setFlash("restarting "+r.ID+"…", false)
		st, restart := m.st, m.hooks.Restart
		return func() tea.Msg {
			nr, err := restart(st, r)
			if err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{text: "started " + nr.ID}
		}
	}
	if !r.Status.Terminal() {
		m.ask(fmt.Sprintf("Restart %s? The current run will be stopped.", runLabel(r)), do)
		return nil
	}
	return do()
}

func (m *model) confirmDelete(r *store.Run) tea.Cmd {
	if !r.Status.Terminal() {
		m.setFlash("stop "+r.ID+" before deleting it (x)", true)
		return nil
	}
	m.ask(fmt.Sprintf("Delete %s and its log?", runLabel(r)), func() tea.Cmd {
		st := m.st
		return func() tea.Msg {
			if err := st.Delete(r.ID); err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{text: "deleted " + r.ID}
		}
	})
	return nil
}

func (m *model) ask(text string, do func() tea.Cmd) {
	m.confirmText, m.confirmDo, m.overlay = text, do, ovConfirm
}

func (m *model) startRun(command string) tea.Cmd {
	st, cwd := m.st, m.cwd
	return func() tea.Msg {
		r, err := supervisor.Create(st, supervisor.Options{Argv: []string{command}, UseShell: true, Cwd: cwd, Detach: true})
		if err != nil {
			return actionMsg{err: err}
		}
		r, err = supervisor.SpawnDetached(st, r)
		if err != nil {
			return actionMsg{err: err}
		}
		if r.Status == store.StatusFailed {
			return actionMsg{err: errors.New(r.ID + " failed to start: " + r.Error)}
		}
		return actionMsg{text: "started " + r.ID}
	}
}

// ---- helpers ----

func matches(filter string, fields ...string) bool {
	if filter == "" {
		return true
	}
	f := strings.ToLower(filter)
	for _, s := range fields {
		if strings.Contains(strings.ToLower(s), f) {
			return true
		}
	}
	return false
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(v, hi))
}

func runLabel(r *store.Run) string {
	if r.Name != "" {
		return r.ID + " (" + r.Name + ")"
	}
	return r.ID
}

func taskTitle(t *claude.Task) string {
	if t.Description != "" {
		return t.Description
	}
	if t.Command != "" {
		return oneLine(claude.ShortCommand(t.Command))
	}
	return "task " + t.ID
}
