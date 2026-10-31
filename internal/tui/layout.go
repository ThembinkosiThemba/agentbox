package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func width(s string) int { return ansi.StringWidth(s) }

// fit truncates or pads s to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	if pad := w - width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
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
	title      string
	right      string
	sub        string
	subRight   string
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
	if selected {
		titleStyle = sBold
	}
	gc := r.glyphColor
	if gc == nil {
		gc = cMuted
	}
	glyph := seg(lipgloss.NewStyle().Foreground(gc), fit(r.glyph, 1)) + pad(1)

	avail := w - 4
	right := trunc(r.right, max(avail/3, 0))
	title := trunc(oneLine(r.title), max(avail-width(right)-1, 0))
	l1 := bar + glyph + seg(titleStyle, title) + pad(avail-width(title)-width(right)) + seg(sMuted, right)

	subRight := trunc(r.subRight, max(avail/3, 0))
	sub := trunc(oneLine(r.sub), max(avail-width(subRight)-1, 0))
	l2 := bar + pad(2) + seg(sMuted, sub) + pad(avail-width(sub)-width(subRight)) + seg(sFaint, subRight)
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

// sanitize makes raw process output safe to draw: escape sequences go,
// carriage-return progress bars keep their final state, tabs expand.
func sanitize(s string) string {
	s = ansi.Strip(s)
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
		for _, r := range s {
			switch {
			case r == '\t':
				n := 4 - col%4
				b.WriteString(strings.Repeat(" ", n))
				col += n
			case r < 0x20 || r == 0x7f:
			default:
				b.WriteRune(r)
				col++
			}
		}
		s = b.String()
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
			out = append(out, l)
			continue
		}
		out = append(out, strings.Split(ansi.Wrap(l, w, ""), "\n")...)
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }
