package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"agentbox/internal/claude"
	"agentbox/internal/human"
	"agentbox/internal/store"
)

const wideLayout = 100

func (m *model) View() string {
	if m.w < 40 || m.h < 10 {
		return "agentbox needs a larger terminal (min 40×10)"
	}
	bodyH := m.h - 2
	var body string
	switch m.screen {
	case screenLog:
		body = m.viewLog(m.w, bodyH)
	case screenSession:
		body = m.viewSession(m.w, bodyH)
	default:
		if m.tab == tabRuns {
			body = m.viewRuns(m.w, bodyH)
		} else {
			body = m.viewSessions(m.w, bodyH)
		}
	}
	switch m.overlay {
	case ovHelp:
		body = overlayOn(body, m.viewHelp(), m.w, bodyH)
	case ovConfirm:
		body = overlayOn(body, m.viewConfirm(), m.w, bodyH)
	case ovInput:
		body = overlayOn(body, m.viewInput(), m.w, bodyH)
	}
	return m.viewHeader() + "\n" + body + "\n" + m.viewFooter()
}

// ---- header & footer ----

func (m *model) viewHeader() string {
	running := 0
	for _, r := range m.runs {
		if !r.Status.Terminal() {
			running++
		}
	}
	active, tasks := 0, 0
	for _, s := range m.sessions {
		if s.Active(m.now) {
			active++
		}
		tasks += s.RunningTasks(m.now)
	}

	tabLabel := func(t tab, name string, n int) string {
		label := fmt.Sprintf(" %s %d ", name, n)
		if m.tab == t {
			return sTabOn.Underline(true).Render(label)
		}
		return sTabOff.Render(label)
	}
	logo := sLogo.Render("agentbox") + "  "
	runsTab, claudeTab := tabLabel(tabRuns, "Runs", len(m.runs)), tabLabel(tabClaude, "Claude", len(m.sessions))
	left := logo + runsTab + " " + claudeTab
	x := width(logo)
	m.tabHits[tabRuns] = [2]int{x, x + width(runsTab)}
	x += width(runsTab) + 1
	m.tabHits[tabClaude] = [2]int{x, x + width(claudeTab)}

	var stats []string
	if running > 0 {
		stats = append(stats, sYellow.Render(m.spin()+" "+itoa(running)+" running"))
	}
	if tasks > 0 {
		stats = append(stats, sBlue.Render("⇢ "+itoa(tasks)+" claude tasks"))
	}
	dot := sFaint.Render("○")
	if m.live > 0 {
		dot = sGreen.Render("●")
	}
	stats = append(stats, dot+sMuted.Render(fmt.Sprintf(" %d claude live · %d active", m.live, active)))
	stats = append(stats, sMuted.Render(m.now.Format("15:04:05")))
	right := strings.Join(stats, sFaint.Render("  │  "))

	gap := m.w - width(left) - width(right) - 1
	if gap < 1 {
		return fit(left, m.w)
	}
	return left + strings.Repeat(" ", gap) + right + " "
}

type hint struct{ key, desc string }

func (m *model) viewFooter() string {
	var hints []hint
	switch {
	case m.overlay == ovInput:
		hints = []hint{{"enter", "confirm"}, {"esc", "cancel"}}
	case m.overlay == ovConfirm:
		hints = []hint{{"y", "yes"}, {"n", "no"}}
	case m.screen == screenLog:
		follow := "follow"
		if m.log.follow {
			follow = "unfollow"
		}
		hints = []hint{{"↑↓", "scroll"}, {"g/G", "top/end"}, {"f", follow}, {"w", "wrap"}}
		if m.log.runID != "" {
			hints = append(hints, hint{"x", "stop"}, hint{"r", "restart"})
		}
		hints = append(hints, hint{"esc", "back"})
	case m.screen == screenSession:
		hints = []hint{{"↑↓", "select"}, {"enter", "task output"}, {"/", "filter"}, {"esc", "back"}}
	case m.tab == tabRuns:
		hints = []hint{{"←→", "tabs"}, {"↑↓", "select"}, {"enter", "logs"}, {"n", "new"}, {"x", "stop"}, {"r", "restart"},
			{"d", "delete"}, {"/", "filter"}}
	default:
		hints = []hint{{"←→", "tabs"}, {"↑↓", "select"}, {"enter", "open"}, {"/", "filter"}}
	}
	hints = append(hints, hint{"?", "help"}, hint{"q", "quit"})

	var parts []string
	for _, h := range hints {
		parts = append(parts, sKey.Render(h.key)+" "+sMuted.Render(h.desc))
	}
	right := strings.Join(parts, "  ")

	left := ""
	if m.filter != "" && m.overlay != ovInput {
		left = sAccent.Render("filter: "+m.filter) + "  "
	}
	if m.flash != "" && m.now.Before(m.flashUntil) {
		st := sAccentB
		if m.flashErr {
			st = sRed.Bold(true)
		}
		left += st.Render(m.flash) + "  "
	}
	line := " " + left + right
	if width(line) > m.w {
		line = trunc(line, m.w)
	}
	return line
}

// ---- runs tab ----

func (m *model) split(w, h int) (horizontal bool, lw, lh, rw, rh int) {
	if w >= wideLayout {
		lw = clamp(w*2/5, 38, 64)
		return true, lw, h, w - lw - 1, h
	}
	lh = clamp(h*2/5, 6, h-6)
	return false, w, lh, w, h - lh
}

func join(horizontal bool, a, b string) string {
	if horizontal {
		return lipgloss.JoinHorizontal(lipgloss.Top, a, " ", b)
	}
	return a + "\n" + b
}

func (m *model) viewRuns(w, h int) string {
	runs := m.visibleRuns()
	horiz, lw, lh, rw, rh := m.split(w, h)

	var listLines []string
	if len(runs) == 0 {
		listLines = m.runsEmpty(lw-4, lh-2)
	} else {
		rows := make([]row, len(runs))
		for i, r := range runs {
			glyph, c, _ := m.runLook(r.Status)
			parts := []string{r.ID}
			if r.Name != "" && r.Title != "" {
				parts = append(parts, r.Name)
			}
			parts = append(parts, baseName(r.Cwd))
			if r.Title != "" || r.Name != "" {
				parts = append(parts, oneLine(r.CommandLine()))
			}
			rw := row{glyph: glyph, glyphColor: c, title: r.DisplayName(), right: human.Duration(r.Duration(m.now)),
				sub: strings.Join(parts, " · "), subRight: whoShort(r.Agent) + " · " + human.Ago(r.CreatedAt, m.now)}
			if x := r.ExitText(); x != "" && x != "0" {
				rw.subRight, rw.subRightColor = "exit "+x, cRed
			}
			rows[i] = rw
		}
		listLines = renderList(rows, m.runSel, &m.runOff, lw-4, lh-2, true)
	}
	count := ""
	if len(runs) > 0 {
		count = fmt.Sprintf("%d/%d", m.runSel+1, len(runs))
	}
	left := panel("Runs", count, listLines, lw, lh, true)

	var detail []string
	title := "Details"
	if r := m.selectedRun(); r != nil {
		title = r.DisplayName()
		detail = m.runDetail(r, rw-4, rh-2)
	}
	right := panel(title, "", detail, rw, rh, false)
	return join(horiz, left, right)
}

func (m *model) runsEmpty(w, h int) []string {
	lines := []string{
		sBold.Render("No runs yet"),
		"",
		sMuted.Render("Agents start runs with"),
		sAccent.Render(`agentbox run -d -- "npm test"`),
		"",
		sMuted.Render("or press ") + sKey.Render("n") + sMuted.Render(" to start one here."),
		"",
		sFaint.Render("agentbox guide  → instructions for agents"),
	}
	if m.filter != "" {
		lines = []string{sMuted.Render("No runs match “" + m.filter + "”")}
	}
	return center(lines, w, h)
}

func (m *model) runDetail(r *store.Run, w, h int) []string {
	_, c, label := m.runLook(r.Status)
	head := pill(label, c) + "  " + sBold.Render(human.Duration(r.Duration(m.now)))
	if x := r.ExitText(); x != "" {
		head += sMuted.Render("  exit " + x)
	}
	lines := []string{head, ""}
	if r.Title != "" {
		lines = append(lines, sBold.Render(trunc(r.Title, w)))
	}
	for i, l := range wrap([]string{r.CommandLine()}, w-2) {
		if i == 3 {
			lines = append(lines, sFaint.Render("  …"))
			break
		}
		p := "  "
		if i == 0 {
			p = sAccent.Render("$ ")
		}
		lines = append(lines, p+sText.Render(l))
	}
	lines = append(lines, "")

	kv := func(k, v string) {
		if v != "" {
			lines = append(lines, sMuted.Render(fmt.Sprintf("%-8s", k))+sText.Render(trunc(v, w-8)))
		}
	}
	if r.Title != "" {
		kv("name", r.Name)
	}
	kv("cwd", r.Cwd)
	started := human.Clock(r.CreatedAt, m.now)
	if r.EndedAt != nil {
		started += sMuted.Render("  →  ") + human.Clock(*r.EndedAt, m.now)
	}
	kv("time", started)
	by := "you"
	if r.Agent != "" {
		by = r.Agent
		if s := m.sessionByID(r.SessionID); s != nil {
			by += sMuted.Render(" · ") + s.DisplayTitle()
		} else if r.SessionID != "" {
			by += sMuted.Render(" · session ") + short(r.SessionID)
		}
	}
	kv("by", by)
	if r.PID > 0 {
		pid := itoa(r.PID)
		if r.Shell != nil {
			pid += sMuted.Render(" · via " + r.Shell[0])
		}
		kv("pid", pid)
	}
	if r.TimeoutSec > 0 {
		kv("timeout", human.Duration(time.Duration(r.TimeoutSec)*time.Second))
	}
	if r.RestartOf != "" {
		kv("restart", "of "+r.RestartOf)
	}
	if r.Error != "" {
		lines = append(lines, sRed.Render(trunc("! "+r.Error, w)))
	}
	lines = append(lines, "")
	return m.appendOutput(lines, w, h, "output", "no output yet")
}

// appendOutput fills the rest of a pane with the tail of the preview log.
func (m *model) appendOutput(lines []string, w, h int, label, empty string) []string {
	lines = append(lines, section(label, "", w))
	room := h - len(lines)
	if room <= 0 {
		return lines[:h]
	}
	if len(m.preview.lines) == 0 {
		return append(lines, sFaint.Render(empty))
	}
	out := wrap(m.preview.lines, w)
	if len(out) > room {
		out = out[len(out)-room:]
	}
	for _, l := range out {
		lines = append(lines, sText.Render(l))
	}
	return lines
}

// ---- claude tab ----

func (m *model) viewSessions(w, h int) string {
	ss := m.visibleSessions()
	horiz, lw, lh, rw, rh := m.split(w, h)

	var listLines []string
	if len(ss) == 0 {
		msg := []string{sBold.Render("No Claude Code sessions"), "", sMuted.Render("Looked in " + claude.ConfigDir())}
		if !m.loaded {
			msg = []string{sMuted.Render(m.spin() + " reading sessions…")}
		} else if m.filter != "" {
			msg = []string{sMuted.Render("No sessions match “" + m.filter + "”")}
		}
		listLines = center(msg, lw-4, lh-2)
	} else {
		rows := make([]row, len(ss))
		for i, s := range ss {
			glyph, c := "○", lipgloss.TerminalColor(cFaint)
			if s.Active(m.now) {
				glyph, c = "●", cGreen
			}
			sub := baseName(s.Cwd)
			if s.Branch != "" && s.Branch != "HEAD" {
				sub += " ⎇ " + s.Branch
			}
			subRight := itoa(len(s.Execs)) + " cmds"
			if n := s.RunningTasks(m.now); n > 0 {
				subRight = m.spin() + " " + itoa(n) + " tasks"
			} else if len(s.Tasks) > 0 {
				subRight += " · " + itoa(len(s.Tasks)) + " bg"
			}
			rows[i] = row{glyph: glyph, glyphColor: c, title: s.DisplayTitle(), right: human.Ago(s.ModTime, m.now),
				sub: sub, subRight: subRight}
		}
		listLines = renderList(rows, m.sesSel, &m.sesOff, lw-4, lh-2, true)
	}
	count := ""
	if len(ss) > 0 {
		count = fmt.Sprintf("%d/%d", m.sesSel+1, len(ss))
	}
	left := panel("Claude Code sessions", count, listLines, lw, lh, true)

	var detail []string
	title := "Session"
	if s := m.selectedSession(); s != nil {
		title = s.DisplayTitle()
		detail = m.sessionSummary(s, rw-4, rh-2)
	}
	return join(horiz, left, panel(title, "", detail, rw, rh, false))
}

func (m *model) sessionSummary(s *claude.Session, w, h int) []string {
	state := pill("IDLE", cMuted)
	if s.Active(m.now) {
		state = pill("ACTIVE", cGreen)
	}
	lines := []string{state + "  " + sMuted.Render("last activity "+human.Ago(s.ModTime, m.now)), ""}
	kv := func(k, v string) {
		if v != "" {
			lines = append(lines, sMuted.Render(fmt.Sprintf("%-9s", k))+sText.Render(trunc(v, w-9)))
		}
	}
	kv("project", s.Cwd)
	kv("branch", s.Branch)
	kv("model", s.Model)
	meta := s.Entrypoint
	if s.Version != "" {
		meta = strings.TrimPrefix(meta+" · v"+s.Version, " · ")
	}
	kv("client", meta)
	kv("started", human.Clock(s.StartedAt, m.now)+sMuted.Render(" · ")+human.Duration(s.LastActivity.Sub(s.StartedAt)))
	usage := fmt.Sprintf("%d prompts · %d turns", s.Prompts, s.Turns)
	if s.InputTokens+s.OutputTokens > 0 {
		usage += fmt.Sprintf(" · %s in · %s out", human.Tokens(s.InputTokens), human.Tokens(s.OutputTokens))
	}
	if s.CostUSD > 0 {
		usage += fmt.Sprintf(" · $%.2f", s.CostUSD)
	}
	kv("usage", usage)
	lines = append(lines, "")

	if act := m.activity(s, w); act != nil {
		lines = append(lines, section("commands over time", "", w))
		lines = append(lines, act...)
		lines = append(lines, "")
	}
	if mix := toolMix(s.ToolCounts, w); mix != nil {
		total := 0
		for _, n := range s.ToolCounts {
			total += n
		}
		lines = append(lines, section("tool mix", sMuted.Render(itoa(total)+" calls"), w))
		lines = append(lines, mix...)
		lines = append(lines, "")
	}

	if len(s.Tasks) > 0 {
		running := s.RunningTasks(m.now)
		note := ""
		if running > 0 {
			note = sYellow.Render(itoa(running) + " running")
		}
		lines = append(lines, section(fmt.Sprintf("background tasks · %d", len(s.Tasks)), note, w))
		for i := len(s.Tasks) - 1; i >= 0 && i >= len(s.Tasks)-4; i-- {
			t := s.Tasks[i]
			g, c, _ := m.taskLook(t.State(s, m.now))
			hint := ""
			if t.Description != "" && t.Command != "" {
				hint = oneLine(claude.ShortCommand(t.Command))
			}
			lines = append(lines, m.cmdLine(t.Start, g, c, "", taskTitle(t), hint, human.Duration(t.Duration(s, m.now)), w))
		}
		lines = append(lines, "")
	}

	if len(s.Execs) > 0 {
		ok, failed, stopped := 0, 0, 0
		for _, e := range s.Execs {
			switch {
			case !e.Done():
			case e.Interrupted:
				stopped++
			case e.IsError:
				failed++
			default:
				ok++
			}
		}
		note := sGreen.Render("✓ " + itoa(ok))
		if failed > 0 {
			note += "  " + sRed.Render("✗ "+itoa(failed))
		}
		if stopped > 0 {
			note += "  " + sMuted.Render("■ "+itoa(stopped))
		}
		lines = append(lines, section(fmt.Sprintf("recent commands · %d", len(s.Execs)), note, w))
		room := h - len(lines) - 2
		for i := len(s.Execs) - 1; i >= 0 && room > 0; i-- {
			e := s.Execs[i]
			g, c := m.execLook(e)
			badge, title, hint := m.execLabel(e)
			lines = append(lines, m.cmdLine(e.Start, g, c, badge, title, hint, human.Duration(e.Duration(m.now)), w))
			room--
		}
	}
	if len(lines) < h-1 {
		for len(lines) < h-1 {
			lines = append(lines, "")
		}
		lines = append(lines, sFaint.Render("resume  ")+sMuted.Render("claude --resume "+s.ID))
	}
	return lines
}

// ---- session detail ----

func (m *model) viewSession(w, h int) string {
	s := m.detSession
	if s == nil {
		return ""
	}
	items := m.detailItems()
	horiz, lw, lh, rw, rh := m.split(w, h)

	var listLines []string
	if len(items) == 0 {
		msg := "This session has not run any commands"
		if m.filter != "" {
			msg = "Nothing matches “" + m.filter + "”"
		}
		listLines = center([]string{sMuted.Render(msg)}, lw-4, lh-2)
	} else {
		rows := make([]row, len(items))
		for i, it := range items {
			if t := it.task; t != nil {
				g, c, _ := m.taskLook(t.State(s, m.now))
				sub := "bg " + t.ID
				if t.Command != "" {
					sub += " · " + oneLine(claude.ShortCommand(t.Command))
				}
				rows[i] = row{glyph: g, glyphColor: c, title: taskTitle(t), right: human.Duration(t.Duration(s, m.now)),
					sub: sub, subRight: human.Clock(t.Start, m.now)}
				continue
			}
			e := it.exec
			g, c := m.execLook(e)
			badge, title, hint := m.execLabel(e)
			sub := e.Tool
			if hint != "" || badge != "" || e.Description != "" {
				sub += " · " + oneLine(claude.ShortCommand(e.Command))
			}
			rw := row{glyph: g, glyphColor: c, badge: badge, title: title, right: human.Duration(e.Duration(m.now)),
				sub: sub, subRight: human.Clock(e.Start, m.now)}
			if e.IsError && e.Done() {
				if l := errorLine(e.Output); l != "" {
					rw.sub, rw.subColor = l, cRed
				}
			}
			rows[i] = rw
		}
		listLines = renderList(rows, m.detSel, &m.detOff, lw-4, lh-2, true)
	}
	right := fmt.Sprintf("%d tasks · %d cmds", len(s.Tasks), len(s.Execs))
	left := panel(s.DisplayTitle(), right, listLines, lw, lh, true)

	var detail []string
	title := "Details"
	if m.detSel < len(items) {
		it := items[m.detSel]
		if it.task != nil {
			title = "Background task " + it.task.ID
			detail = m.taskDetail(s, it.task, rw-4, rh-2)
		} else {
			title = it.exec.Tool + " command"
			detail = m.execDetail(it.exec, rw-4, rh-2)
		}
	}
	return join(horiz, left, panel(title, "", detail, rw, rh, false))
}

func (m *model) commandLines(cmd string, w, maxLines int) []string {
	var out []string
	src := strings.Split(strings.TrimRight(cmd, "\n"), "\n")
	for i, l := range wrap(src, w-2) {
		if i == maxLines {
			out = append(out, sFaint.Render("  …"))
			break
		}
		p := "  "
		if i == 0 {
			p = sAccent.Render("$ ")
		}
		out = append(out, p+sText.Render(sanitize(l)))
	}
	return out
}

func (m *model) taskDetail(s *claude.Session, t *claude.Task, w, h int) []string {
	_, c, label := m.taskLook(t.State(s, m.now))
	head := pill(label, c) + "  " + sBold.Render(human.Duration(t.Duration(s, m.now)))
	if t.ExitCode != nil {
		head += sMuted.Render("  exit " + itoa(*t.ExitCode))
	}
	lines := []string{head, ""}
	if t.Description != "" {
		lines = append(lines, sText.Render(trunc(t.Description, w)))
	}
	if t.Command != "" {
		lines = append(lines, m.commandLines(t.Command, w, 4)...)
	}
	lines = append(lines, "")
	if t.Summary != "" && t.Summary != t.Description {
		lines = append(lines, sMuted.Render(trunc(t.Summary, w)))
	}
	if t.OutputFile != "" {
		lines = append(lines, sFaint.Render(trunc(t.OutputFile, w)))
	}
	if t.State(s, m.now) == claude.TaskUnknown {
		lines = append(lines, sFaint.Render("No completion notice was recorded; the session may have ended first."))
	}
	lines = append(lines, "")
	empty := "no output file"
	if t.OutputFile != "" {
		empty = "output file is empty or was cleaned up"
	}
	return m.appendOutput(lines, w, h, "output · enter for full view", empty)
}

func (m *model) execDetail(e *claude.Exec, w, h int) []string {
	label, c := "OK", lipgloss.TerminalColor(cGreen)
	switch {
	case !e.Done():
		label, c = "RUNNING", cYellow
	case e.Interrupted:
		label, c = "INTERRUPTED", cMuted
	case e.IsError:
		label, c = "ERROR", cRed
	}
	lines := []string{pill(label, c) + "  " + sBold.Render(human.Duration(e.Duration(m.now))) +
		sMuted.Render("  at "+human.Clock(e.Start, m.now)), ""}
	if e.Description != "" {
		lines = append(lines, sText.Render(trunc(e.Description, w)))
	}
	if r := m.callRun(e); r != nil {
		g, c, label := m.runLook(r.Status)
		lines = append(lines, sBadge.Render("▣ agentbox ")+fg(c).Render(g+" "+strings.ToLower(label))+" "+
			sText.Render(trunc(r.ID+" · "+r.DisplayName(), w-width(label)-16)))
	}
	lines = append(lines, m.commandLines(e.Command, w, 8)...)
	lines = append(lines, "")
	lines = append(lines, section("result", "", w))
	out := strings.TrimSpace(e.Output)
	if out == "" {
		if e.Done() {
			return append(lines, sFaint.Render("(no output)"))
		}
		return append(lines, sFaint.Render(m.spin()+" waiting for result…"))
	}
	src := strings.Split(out, "\n")
	for i := range src {
		src[i] = sanitize(src[i])
	}
	st := sText
	if e.IsError {
		st = sRed
	}
	for _, l := range wrap(src, w) {
		if len(lines) >= h {
			break
		}
		lines = append(lines, st.Render(l))
	}
	return lines
}

// ---- full-screen log ----

func (m *model) viewLog(w, h int) string {
	l := &m.log
	status := ""
	switch {
	case l.runID != "":
		if r := m.runByID(l.runID); r != nil {
			g, c, label := m.runLook(r.Status)
			status = lipgloss.NewStyle().Foreground(c).Render(g+" "+strings.ToLower(label)) + " " + human.Duration(r.Duration(m.now))
		}
	case l.taskID != "":
		if s, t := m.taskByID(l.sessionID, l.taskID); t != nil {
			g, c, label := m.taskLook(t.State(s, m.now))
			status = lipgloss.NewStyle().Foreground(c).Render(g+" "+strings.ToLower(label)) + " " + human.Duration(t.Duration(s, m.now))
		}
	}

	iw, ih := w-4, h-2
	lines := l.lines
	if l.wrap {
		lines = wrap(lines, iw)
	}
	maxOff := max(len(lines)-ih, 0)
	if l.follow || l.off > maxOff {
		l.off = maxOff
	}
	end := min(l.off+ih, len(lines))
	view := make([]string, 0, ih)
	for _, s := range lines[l.off:end] {
		view = append(view, sText.Render(s))
	}
	if len(lines) == 0 {
		view = center([]string{sFaint.Render("no output yet")}, iw, ih)
	}

	pos := "end"
	if len(lines) > ih {
		pos = fmt.Sprintf("%d%%", (end*100)/len(lines))
	}
	flags := []string{pos}
	if l.follow {
		flags = append(flags, "following")
	}
	if l.truncated {
		flags = append(flags, "tail")
	}
	right := strings.Join(flags, " · ")
	if status != "" {
		right = status + sMuted.Render(" · ") + right
	}
	return panel(l.title, right, view, w, h, true)
}

// ---- overlays ----

func box(title string, lines []string, w int) string {
	return panel(title, "", lines, w, len(lines)+2, true)
}

func (m *model) viewHelp() string {
	type kv struct{ k, v string }
	groups := []struct {
		name string
		keys []kv
	}{
		{"Everywhere", []kv{{"← → · tab", "switch between Runs and Claude"}, {"↑↓ · j k", "move"}, {"g · G", "first · last"},
			{"enter", "open"}, {"esc", "back / clear filter"}, {"/", "filter"}, {"q", "quit"}}},
		{"Runs", []kv{{"n", "start a command here"}, {"x", "stop the run and its process tree"}, {"r", "restart"}, {"d", "delete"}}},
		{"Logs", []kv{{"f", "follow output"}, {"w", "wrap lines"}, {"PgUp PgDn", "page"}}},
	}
	var lines []string
	for i, g := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, sAccentB.Render(g.name))
		for _, k := range g.keys {
			lines = append(lines, "  "+sKey.Render(fit(k.k, 13))+sMuted.Render(k.v))
		}
	}
	lines = append(lines, "", sFaint.Render("agentbox "+m.hooks.Version+" · press any key"))
	return box("Keys", lines, min(54, m.w))
}

func (m *model) viewConfirm() string {
	w := clamp(width(m.confirmText)+6, 36, m.w-4)
	lines := []string{sText.Render(m.confirmText), "", sKey.Render("y") + sMuted.Render(" yes   ") + sKey.Render("n") + sMuted.Render(" no")}
	return box("Confirm", lines, w)
}

func (m *model) viewInput() string {
	w := clamp(m.w*2/3, 40, 80)
	m.input.Width = w - 8
	var lines []string
	title := "Filter"
	if m.inputPurpose == "new" {
		title = "New run"
		lines = append(lines, sMuted.Render("Runs detached through the shell in"), sText.Render(trunc(m.cwd, w-4)), "")
	}
	lines = append(lines, m.input.View())
	return box(title, lines, w)
}

// overlayOn draws fg centred over bg.
func overlayOn(bg, fg string, w, h int) string {
	bgLines := strings.Split(bg, "\n")
	for len(bgLines) < h {
		bgLines = append(bgLines, "")
	}
	fgLines := strings.Split(fg, "\n")
	fw := 0
	for _, l := range fgLines {
		fw = max(fw, width(l))
	}
	x := max((w-fw)/2, 0)
	y := max((h-len(fgLines))/2, 0)
	for i, fl := range fgLines {
		j := y + i
		if j >= len(bgLines) {
			break
		}
		b := fit(bgLines[j], w)
		left := ansi.Truncate(b, x, "")
		right := ansi.TruncateLeft(b, x+fw, "")
		bgLines[j] = sFaint.Render(ansi.Strip(left)) + fl + sFaint.Render(ansi.Strip(right))
	}
	// Dim the rest so the dialog stands out.
	for j := range bgLines {
		if j < y || j >= y+len(fgLines) {
			bgLines[j] = sFaint.Render(ansi.Strip(bgLines[j]))
		}
	}
	return strings.Join(bgLines[:h], "\n")
}

// callRun is the agentbox run a command started or referred to, if it is
// still on record. `run -d` prints the new id first, which identifies runs
// started without a name.
func (m *model) callRun(e *claude.Exec) *store.Run {
	c := parseAgentbox(claude.ShortCommand(e.Command))
	if c == nil {
		return nil
	}
	if c.sub == "run" {
		if f := strings.Fields(e.Output); len(f) > 0 {
			if r := m.runByID(f[0]); r != nil {
				return r
			}
		}
		return findRun(m.runs, c.name)
	}
	return findRun(m.runs, c.ref)
}

func whoShort(agent string) string {
	switch agent {
	case "":
		return "you"
	case "claude-code":
		return "claude"
	}
	return agent
}

func (m *model) sessionByID(id string) *claude.Session {
	if id == "" {
		return nil
	}
	for _, s := range m.sessions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func baseName(p string) string {
	if p == "" {
		return "—"
	}
	return filepath.Base(strings.ReplaceAll(p, `\`, "/"))
}
