package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agentbox/internal/agy"
	"agentbox/internal/claude"
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

func TestTabKeysCycle(t *testing.T) {
	m := fixture(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	press := func(k tea.KeyMsg, want tab) {
		t.Helper()
		m.Update(k)
		if m.tab != want {
			t.Errorf("%q: tab = %d, want %d", k.String(), m.tab, want)
		}
	}
	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")}, tabAgy)
	press(tea.KeyMsg{Type: tea.KeyTab}, tabRuns)
	press(tea.KeyMsg{Type: tea.KeyShiftTab}, tabAgy)
	press(tea.KeyMsg{Type: tea.KeyShiftTab}, tabClaude)
	press(tea.KeyMsg{Type: tea.KeyRight}, tabAgy)
	press(tea.KeyMsg{Type: tea.KeyRight}, tabAgy) // stops at the last tab
	press(tea.KeyMsg{Type: tea.KeyLeft}, tabClaude)
	press(tea.KeyMsg{Type: tea.KeyLeft}, tabRuns)
	press(tea.KeyMsg{Type: tea.KeyLeft}, tabRuns) // and at the first
}

// Each agent's tab lists only its own sessions and keeps its own selection.
func TestSessionTabs(t *testing.T) {
	m := fixture(t)
	m.sessions = append(m.sessions, &claude.Session{Agent: agy.Agent, ID: "second-agy", Title: "Second", ModTime: m.now.Add(-time.Hour)})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	m.switchTab(tabClaude)
	if ss := m.visibleSessions(); len(ss) != 1 || ss[0].Agent != claude.Agent {
		t.Fatalf("Claude tab lists %d sessions", len(ss))
	}
	m.switchTab(tabAgy)
	if ss := m.visibleSessions(); len(ss) != 2 || ss[0].Agent != agy.Agent {
		t.Fatalf("Agy tab lists %d sessions", len(ss))
	}
	if s := m.selectedSession(); s == nil || s.Agent != agy.Agent {
		t.Fatalf("Agy tab selected %+v", s)
	}
	m.move(1)
	if s := m.selectedSession(); s.ID != "second-agy" {
		t.Fatalf("selected %q", s.ID)
	}
	m.switchTab(tabClaude)
	if s := m.selectedSession(); s == nil || s.Agent != claude.Agent {
		t.Fatalf("back on Claude tab selected %+v", s)
	}
	m.switchTab(tabRuns)
	m.switchTab(tabAgy)
	if s := m.selectedSession(); s.ID != "second-agy" {
		t.Errorf("back on Agy tab selected %q, want second-agy", s.ID)
	}

	m.open()
	if m.screen != screenSession || m.detSession.ID != "second-agy" {
		t.Errorf("open: screen = %d, session = %+v", m.screen, m.detSession)
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
	click(m.tabHits[tabAgy][0]+1, 0)
	if m.tab != tabAgy {
		t.Fatalf("clicking Agy label: tab = %d", m.tab)
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
