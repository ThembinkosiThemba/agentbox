package tui

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func width(s string) int { return ansi.StringWidth(s) }

// fit truncates or pads s to exactly w cells. Colour is closed before any
// padding goes on, so the terminal's own colours fill the gap.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if width(s) > w {
		s = seal(ansi.Truncate(s, w, "…"))
	}
	if pad := w - width(s); pad > 0 {
		s = seal(s) + strings.Repeat(" ", pad)
	}
	return s
}

func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if width(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return s
}

// oneLine collapses whitespace so multi-line commands fit in a row.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// panel draws a rounded box of exactly w×h with title (and optional right
// label) set into the top border.
func panel(title, right string, lines []string, w, h int, focused bool) string {
	if w < 6 || h < 2 {
		return ""
	}
	bc := sBorder
	ts := sMuted.Bold(true)
	if focused {
		bc = lipgloss.NewStyle().Foreground(cAccent)
		ts = sAccentB
	}
	iw := w - 4
	title = trunc(title, iw-2)
	head := bc.Render("╭─ ") + ts.Render(title) + " "
	tail := bc.Render("╮")
	if right != "" {
		right = " " + sMuted.Render(right) + " " + bc.Render("─")
	}
	fill := w - width(head) - width(right) - width(tail)
	if fill < 0 {
		right, fill = "", w-width(head)-width(tail)
	}
	var b strings.Builder
	b.WriteString(head + bc.Render(strings.Repeat("─", max(fill, 0))) + right + tail + "\n")
	side := bc.Render("│")
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		b.WriteString(side + " " + fit(l, iw) + " " + side + "\n")
	}
	b.WriteString(bc.Render("╰" + strings.Repeat("─", w-2) + "╯"))
	return b.String()
}

// row is one two-line list entry.
type row struct {
	glyph      string
	glyphColor lipgloss.TerminalColor
	badge      string // short tag before the title, e.g. "agentbox"
	title      string
	hint       string // faint text after the title, when there is room
	right      string
	subLead    string // first part of the second line, coloured on its own: an id or a tool
	sub        string
	subRight   string

	titleColor              lipgloss.TerminalColor // default text colour
	subColor, subRightColor lipgloss.TerminalColor // default muted, faint
	subLeadColor            lipgloss.TerminalColor // default cyan
}

// renderList draws rows into w×h cells, keeping sel visible by adjusting
// *off, the index of the first visible row.
func renderList(rows []row, sel int, off *int, w, h int, focused bool) []string {
	per := 2
	visible := max(h/per, 1)
	if sel < *off {
		*off = sel
	}
	if sel >= *off+visible {
		*off = sel - visible + 1
	}
	if *off > max(len(rows)-visible, 0) {
		*off = max(len(rows)-visible, 0)
	}
	if *off < 0 {
		*off = 0
	}
	var out []string
	for i := *off; i < len(rows) && len(out)+per <= h; i++ {
		out = append(out, renderRow(rows[i], w, i == sel, focused)...)
	}
	// Scroll hint on the last line when more rows are below.
	if rest := len(rows) - (*off + visible); rest > 0 && len(out) == h && h > 0 {
		out[h-1] = fit(sFaint.Render("  ↓ "+itoa(rest)+" more"), w)
	}
	return out
}

func renderRow(r row, w int, selected, focused bool) []string {
	base := lipgloss.NewStyle()
	bar := "  "
	if selected {
		base = sSelBg
		bs := sFaint
		if focused {
			bs = sAccent
		}
		bar = bs.Inherit(base).Render("▌") + base.Render(" ")
	}
	seg := func(s lipgloss.Style, text string) string { return s.Inherit(base).Render(text) }
	pad := func(n int) string {
		if n <= 0 {
			return ""
		}
		return base.Render(strings.Repeat(" ", n))
	}

	titleStyle := sText
	if r.titleColor != nil {
		titleStyle = fg(r.titleColor)
	}
	if selected {
		titleStyle = titleStyle.Bold(true)
	}
	gc := r.glyphColor
	if gc == nil {
		gc = cMuted
	}
	glyph := seg(lipgloss.NewStyle().Foreground(gc), fit(r.glyph, 1)) + pad(1)

	avail := w - 4
	right := trunc(r.right, max(avail/3, 0))
	room := max(avail-width(right)-1, 0)
	badge := ""
	if r.badge != "" && room > 24 {
		badge = seg(sBadge, "▣ "+r.badge) + pad(1)
		room -= width(badge)
	}
	title := trunc(oneLine(r.title), room)
	hint := ""
	if h := oneLine(r.hint); h != "" && room-width(title) > 8 {
		hint = seg(sFaint, " "+trunc(h, room-width(title)-1))
	}
	l1 := bar + glyph + badge + seg(titleStyle, title) + hint +
		pad(avail-width(badge)-width(title)-width(hint)-width(right)) + seg(sMuted, right)

	subStyle, subRightStyle, subLeadStyle := sMuted, sFaint, sCmd
	if r.subColor != nil {
		subStyle = fg(r.subColor)
	}
	if r.subRightColor != nil {
		subRightStyle = fg(r.subRightColor)
	}
	if r.subLeadColor != nil {
		subLeadStyle = fg(r.subLeadColor)
	}
	subRight := trunc(r.subRight, max(avail/3, 0))
	subLead := ""
	if l := oneLine(r.subLead); l != "" {
		subLead = seg(subLeadStyle, trunc(l, max(avail/3, 0))) + pad(1)
	}
	sub := trunc(oneLine(r.sub), max(avail-width(subLead)-width(subRight)-1, 0))
	l2 := bar + pad(2) + subLead + seg(subStyle, sub) + pad(avail-width(subLead)-width(sub)-width(subRight)) + seg(subRightStyle, subRight)
	return []string{l1, l2}
}

// center places lines in the middle of a w×h area.
func center(lines []string, w, h int) []string {
	out := make([]string, 0, h)
	top := max((h-len(lines))/2, 0)
	for range top {
		out = append(out, "")
	}
	for _, l := range lines {
		out = append(out, strings.Repeat(" ", max((w-width(l))/2, 0))+l)
	}
	return out
}

// sanitize makes raw process output safe to draw: colour (SGR) codes are
// kept so a command's own colours survive, every other escape sequence and
// control character is dropped, carriage-return progress bars keep their
// final state, and tabs expand.
func sanitize(s string) string {
	s = keepColor(s)
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		if rest := s[i+1:]; rest != "" {
			s = rest
		} else {
			s = s[:i]
			if j := strings.LastIndexByte(s, '\r'); j >= 0 {
				s = s[j+1:]
			}
		}
	}
	if strings.ContainsAny(s, "\t\x00\x07\x08\x0b\x0c\x1b") {
		var b strings.Builder
		col := 0
		for i := 0; i < len(s); {
			if isEscape(s[i:]) {
				seq, _, n, _ := ansi.DecodeSequence(s[i:], 0, nil)
				if n <= 0 {
					break
				}
				if isSGR(seq) {
					b.WriteString(seq)
				}
				i += n
				continue
			}
			r, size := utf8.DecodeRuneInString(s[i:])
			i += size
			switch {
			case r == '\t':
				n := 4 - col%4
				b.WriteString(strings.Repeat(" ", n))
				col += n
			case r < 0x20 || r == 0x7f:
			default:
				b.WriteRune(r)
				col += ansi.StringWidth(string(r))
			}
		}
		s = b.String()
	}
	return seal(s)
}

// plain is sanitized text with its colour removed, for output that goes into
// a line that has a colour of its own.
func plain(s string) string { return ansi.Strip(sanitize(s)) }

// keepColor drops ANSI escape sequences from s, except the SGR (colour and
// text attribute) sequences that a command writes to colour its output.
func keepColor(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		seq, _, n, _ := ansi.DecodeSequence(s[i:], 0, nil)
		if n <= 0 {
			break
		}
		if !isEscape(seq) || isSGR(seq) {
			b.WriteString(seq)
		}
		i += n
	}
	return b.String()
}

// isEscape reports whether seq introduces a terminal escape sequence: 7-bit
// (ESC …) or 8-bit (C1 …).
func isEscape(seq string) bool {
	return seq[0] == 0x1b || (seq[0] >= 0x80 && seq[0] <= 0x9f)
}

// isSGR reports whether seq is a colour/text-attribute sequence, the only
// escape sequences that are safe and useful to draw.
func isSGR(seq string) bool {
	if len(seq) < 2 || seq[len(seq)-1] != 'm' {
		return false
	}
	return seq[0] == 0x9b || (seq[0] == 0x1b && seq[1] == '[')
}

// seal closes colour left open at the end of s, so a command's colour can
// never bleed into a panel border or the next line.
func seal(s string) string {
	if strings.Contains(s, "\x1b[") && !strings.HasSuffix(s, "\x1b[0m") {
		return s + "\x1b[0m"
	}
	return s
}

// wrap soft-wraps each line to w cells.
func wrap(lines []string, w int) []string {
	if w <= 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if width(l) <= w {
			out = append(out, seal(l))
			continue
		}
		for _, s := range strings.Split(ansi.Wrap(l, w, ""), "\n") {
			out = append(out, seal(s))
		}
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }
