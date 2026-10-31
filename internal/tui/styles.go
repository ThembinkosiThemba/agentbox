package tui

import (
	"github.com/charmbracelet/lipgloss"

	"agentbox/internal/claude"
	"agentbox/internal/store"
)

// Palette. Adaptive colors pick the light or dark variant from the
// terminal's background.
var (
	cAccent  = lipgloss.AdaptiveColor{Light: "#5B4BDB", Dark: "#A496FF"}
	cText    = lipgloss.AdaptiveColor{Light: "#1F2328", Dark: "#E6EDF3"}
	cMuted   = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#7D8590"}
	cFaint   = lipgloss.AdaptiveColor{Light: "#AFB8C1", Dark: "#484F58"}
	cBorder  = lipgloss.AdaptiveColor{Light: "#D0D7DE", Dark: "#30363D"}
	cSelBg   = lipgloss.AdaptiveColor{Light: "#EEEBFF", Dark: "#1F1B3A"}
	cGreen   = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	cRed     = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"}
	cYellow  = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	cBlue    = lipgloss.AdaptiveColor{Light: "#0969DA", Dark: "#58A6FF"}
	cOnColor = lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#0D1117"}
)

var (
	sText    = lipgloss.NewStyle().Foreground(cText)
	sBold    = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sMuted   = lipgloss.NewStyle().Foreground(cMuted)
	sFaint   = lipgloss.NewStyle().Foreground(cFaint)
	sAccent  = lipgloss.NewStyle().Foreground(cAccent)
	sAccentB = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sGreen   = lipgloss.NewStyle().Foreground(cGreen)
	sRed     = lipgloss.NewStyle().Foreground(cRed)
	sYellow  = lipgloss.NewStyle().Foreground(cYellow)
	sBlue    = lipgloss.NewStyle().Foreground(cBlue)
	sBorder  = lipgloss.NewStyle().Foreground(cBorder)
	sKey     = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sSelBg   = lipgloss.NewStyle().Background(cSelBg)
	sLogo    = lipgloss.NewStyle().Foreground(cOnColor).Background(cAccent).Bold(true).Padding(0, 1)
	sTabOn   = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sTabOff  = lipgloss.NewStyle().Foreground(cMuted)
)

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m *model) spin() string { return spinner[m.frame%len(spinner)] }

func pill(label string, c lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().Foreground(cOnColor).Background(c).Bold(true).Padding(0, 1).Render(label)
}

// runLook maps a run status to its glyph, color and label.
func (m *model) runLook(s store.Status) (glyph string, c lipgloss.TerminalColor, label string) {
	switch s {
	case store.StatusRunning:
		return m.spin(), cYellow, "RUNNING"
	case store.StatusPending:
		return "◌", cMuted, "STARTING"
	case store.StatusSucceeded:
		return "✓", cGreen, "DONE"
	case store.StatusFailed:
		return "✗", cRed, "FAILED"
	case store.StatusKilled:
		return "■", cMuted, "KILLED"
	case store.StatusTimedOut:
		return "◷", cRed, "TIMEOUT"
	}
	return "?", cMuted, "LOST"
}

func (m *model) taskLook(s claude.TaskState) (glyph string, c lipgloss.TerminalColor, label string) {
	switch s {
	case claude.TaskRunning:
		return m.spin(), cYellow, "RUNNING"
	case claude.TaskCompleted:
		return "✓", cGreen, "DONE"
	case claude.TaskFailed:
		return "✗", cRed, "FAILED"
	case claude.TaskKilled:
		return "■", cMuted, "STOPPED"
	}
	return "·", cMuted, "UNKNOWN"
}

func (m *model) execLook(e *claude.Exec) (glyph string, c lipgloss.TerminalColor) {
	switch {
	case !e.Done():
		return m.spin(), cYellow
	case e.Interrupted:
		return "■", cMuted
	case e.IsError:
		return "✗", cRed
	case e.TaskID != "":
		return "⇢", cBlue
	}
	return "✓", cGreen
}
