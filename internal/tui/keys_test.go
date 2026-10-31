package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTabKeys(t *testing.T) {
	m := fixture(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, k := range []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyRunes, Runes: []rune("2")}, {Type: tea.KeyRight}} {
		m.tab = tabRuns
		m.Update(k)
		if m.tab != tabClaude {
			t.Errorf("%q: tab = %d, want claude", k.String(), m.tab)
		}
	}
}

func TestClickTabs(t *testing.T) {
	m := fixture(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.View() // records the tab label positions
	click := func(x, y int) {
		m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	}
	click(m.tabHits[tabClaude][0]+1, 0)
	if m.tab != tabClaude {
		t.Fatalf("clicking Claude label: tab = %d", m.tab)
	}
	click(m.tabHits[tabRuns][0]+1, 0)
	if m.tab != tabRuns {
		t.Fatalf("clicking Runs label: tab = %d", m.tab)
	}
	click(5, 4) // second run's first line
	if m.runSel != 1 {
		t.Fatalf("clicking row 2: runSel = %d", m.runSel)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.tab != tabClaude {
		t.Fatalf("right arrow: tab = %d", m.tab)
	}
}
