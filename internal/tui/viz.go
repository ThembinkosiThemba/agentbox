package tui

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"agentbox/internal/claude"
	"agentbox/internal/human"
)

// section is a labelled rule across the pane, with an optional note on the
// right: ── recent commands ──────── ✓ 27  ✗ 2 ──
func section(name, note string, w int) string {
	left := sFaint.Render("── ") + sMuted.Render(name) + " "
	right := ""
	if note != "" {
		right = " " + note + sFaint.Render(" ──")
	}
	fill := w - width(left) - width(right)
	if fill < 2 {
		right, fill = "", w-width(left)
	}
	return left + sFaint.Render(strings.Repeat("─", max(fill, 0))) + right
}

// toolMix draws how a session split its tool calls as one proportional
// strip, with a legend under it.
func toolMix(counts map[string]int, w int) []string {
	merged := map[string]int{}
	total := 0
	for name, n := range counts {
		merged[toolName(name)] += n
		total += n
	}
	if total == 0 || w < 10 {
		return nil
	}
	type tc struct {
		name string
		n    int
	}
	var tcs []tc
	for k, v := range merged {
		tcs = append(tcs, tc{k, v})
	}
	sort.Slice(tcs, func(i, j int) bool { return tcs[i].n > tcs[j].n || (tcs[i].n == tcs[j].n && tcs[i].name < tcs[j].name) })
	if len(tcs) > len(series) {
		rest := 0
		for _, t := range tcs[len(series)-1:] {
			rest += t.n
		}
		tcs = append(tcs[:len(series)-1], tc{"other", rest})
	}

	var bar strings.Builder
	used := 0
	for i, t := range tcs {
		n := max(t.n*w/total, 1)
		if i == len(tcs)-1 {
			n = w - used
		}
		n = min(n, w-used-(len(tcs)-1-i)) // leave a cell for each remaining tool
		if n <= 0 {
			continue
		}
		bar.WriteString(fg(series[i%len(series)]).Render(strings.Repeat("━", n)))
		used += n
	}

	lines := []string{bar.String()}
	var cur string
	for i, t := range tcs {
		item := fg(series[i%len(series)]).Render("●") + " " + sText.Render(t.name) + " " + sMuted.Render(itoa(t.n))
		switch {
		case cur == "":
			cur = item
		case width(cur)+3+width(item) <= w:
			cur += "   " + item
		default:
			lines = append(lines, cur)
			cur = item
		}
	}
	return append(lines, cur)
}

// toolName shortens MCP tool names to their server, so a session that used
// ten Gmail tools shows one "Gmail" entry.
func toolName(name string) string {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return name
	}
	server, _, _ := strings.Cut(rest, "__")
	server = strings.TrimPrefix(server, "claude_ai_")
	if server == "" {
		return "mcp"
	}
	return server
}

var bars = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// activity is a sparkline of when a session ran commands, red where any
// failed, with the time span under it.
func (m *model) activity(s *claude.Session, w int) []string {
	t0, t1 := s.StartedAt, s.LastActivity
	if s.Active(m.now) {
		t1 = m.now
	}
	span := t1.Sub(t0)
	if len(s.Execs) < 3 || span < time.Minute || w < 20 {
		return nil
	}
	// About two commands per bin, so a short session reads as a histogram
	// rather than scattered ticks; each bin is drawn several cells wide.
	n := clamp(len(s.Execs)/2, 8, w)
	counts, errs, live := make([]int, n), make([]bool, n), make([]bool, n)
	peak := 0
	for _, e := range s.Execs {
		if e.Start.IsZero() {
			continue
		}
		b := clamp(int(e.Start.Sub(t0)*time.Duration(n)/span), 0, n-1)
		counts[b]++
		errs[b] = errs[b] || e.IsError
		live[b] = live[b] || !e.Done()
		peak = max(peak, counts[b])
	}
	var b strings.Builder
	for i, c := range counts {
		cells := (i+1)*w/n - i*w/n
		switch {
		case c == 0:
			b.WriteString(sFaint.Render(strings.Repeat(bars[0], cells)))
		default:
			lvl := len(bars) - 1
			if peak > 1 {
				lvl = 1 + (c-1)*(len(bars)-2)/(peak-1)
			}
			st := sAccent
			switch {
			case live[i]:
				st = sYellow
			case errs[i]:
				st = sRed
			}
			b.WriteString(st.Render(strings.Repeat(bars[lvl], cells)))
		}
	}
	from, to := t0.Local().Format("15:04"), t1.Local().Format("15:04")
	if s.Active(m.now) {
		to = "now"
	}
	if t0.Local().YearDay() != t1.Local().YearDay() {
		from = t0.Local().Format("Jan 2 15:04")
	}
	mid := itoa(len(s.Execs)) + " commands over " + human.Duration(span)
	gap := w - width(from) - width(to)
	axis := sFaint.Render(from) + sFaint.Render(centerText(mid, gap)) + sFaint.Render(to)
	if gap < width(mid)+2 {
		axis = sFaint.Render(from + strings.Repeat(" ", max(gap, 0)) + to)
	}
	return []string{b.String(), axis}
}

func centerText(s string, w int) string {
	if width(s) >= w {
		return strings.Repeat(" ", max(w, 0))
	}
	l := (w - width(s)) / 2
	return strings.Repeat(" ", l) + s + strings.Repeat(" ", w-l-width(s))
}

// execLabel is how a command reads in a list: agentbox calls by their run's
// title, others by Claude's description with the command after it.
func (m *model) execLabel(e *claude.Exec) (badge, title, hint string) {
	short := oneLine(claude.ShortCommand(e.Command))
	if c := parseAgentbox(short); c != nil {
		hint := ""
		if c.sub == "run" && (c.title != "" || c.name != "") {
			hint = oneLine(c.cmd)
		}
		return "agentbox", c.label(m.runs), hint
	}
	if e.Description != "" {
		return "", e.Description, short
	}
	return "", short, ""
}

var reExitLine = regexp.MustCompile(`^Exit code (-?\d+)$`)

// errorLine is the gist of a failed command's output: its exit code and the
// first line that says something.
func errorLine(out string) string {
	code := ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(sanitize(l))
		if l == "" {
			continue
		}
		if mm := reExitLine.FindStringSubmatch(l); mm != nil {
			code = "exit " + mm[1]
			continue
		}
		if code != "" {
			return code + " · " + l
		}
		return l
	}
	return code
}

// cmdLine is one compact command row for summary panes:
// 18:42 ✓ ▣ agentbox run · Build the web app  npm run build        1m26s
func (m *model) cmdLine(at time.Time, glyph string, c lipgloss.TerminalColor, badge, title, hint, dur string, w int) string {
	clock := "     "
	if !at.IsZero() {
		clock = at.Local().Format("15:04")
	}
	left := sFaint.Render(clock) + " " + fg(c).Render(glyph) + " "
	if badge != "" {
		left += sBadge.Render("▣") + " "
	}
	room := w - width(left) - width(dur) - 1
	t := trunc(title, room)
	line := left + sText.Render(t)
	if hint != "" && room-width(t) > 8 {
		line += sFaint.Render("  " + trunc(hint, room-width(t)-2))
	}
	return fit(line, w-width(dur)) + sMuted.Render(dur)
}
