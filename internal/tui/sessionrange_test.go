package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agentbox/internal/agy"
)

func TestParseRange(t *testing.T) {
	good := map[string]time.Duration{
		"45m":   45 * time.Minute,
		"45min": 45 * time.Minute,
		"12h":   12 * time.Hour,
		"14d":   14 * day,
		"14":    14 * day,
		"2w":    14 * day,
		"3mo":   90 * day,
		"1y":    365 * day,
		" 2W ":  14 * day,
		"7 d":   7 * day,
		"all":   allTime,
		"ALL":   allTime,
	}
	for in, want := range good {
		if got, err := parseRange(in); err != nil || got != want {
			t.Errorf("parseRange(%q) = %v, %v, want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "0d", "-3d", "1.5d", "soon", "3x", "d", "99999999999999y", "99999999999999999999"} {
		if got, err := parseRange(in); err == nil {
			t.Errorf("parseRange(%q) = %v, want an error", in, got)
		}
	}
}

func TestFormatRangeRoundTrips(t *testing.T) {
	for _, d := range []time.Duration{45 * time.Minute, 90 * time.Minute, 12 * time.Hour, 36 * time.Hour, day, 14 * day, 365 * day, allTime} {
		got, err := parseRange(formatRange(d))
		if err != nil || got != d {
			t.Errorf("%v -> %q -> %v, %v", d, formatRange(d), got, err)
		}
	}
}

func TestNextRange(t *testing.T) {
	cases := []struct{ cur, want time.Duration }{
		{day, 3 * day},
		{7 * day, 30 * day},
		{90 * day, allTime},
		{allTime, day},
		{time.Hour, day},     // custom, shorter than every preset
		{14 * day, 30 * day}, // custom, between presets
		{365 * day, allTime}, // custom, longer than every preset
	}
	for _, c := range cases {
		if got := nextRange(c.cur); got != c.want {
			t.Errorf("nextRange(%v) = %v, want %v", c.cur, got, c.want)
		}
	}
}

func press(m *model, key string) {
	if key == "enter" {
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return
	}
	if key == "esc" {
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		return
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
}

func TestRangeKeys(t *testing.T) {
	m := fixture(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	press(m, "t") // not on the Runs tab: it has no range
	if m.sessionRange != defaultRange {
		t.Errorf("t on Runs: range = %v", m.sessionRange)
	}
	m.switchTab(tabAgy)
	press(m, "t")
	if m.sessionRange != 30*day {
		t.Errorf("t: range = %v, want 30d", m.sessionRange)
	}

	press(m, "T")
	if m.overlay != ovInput || m.inputPurpose != "range" {
		t.Fatalf("T: overlay = %d, purpose = %q", m.overlay, m.inputPurpose)
	}
	m.input.SetValue("2w")
	press(m, "enter")
	if m.sessionRange != 14*day || m.overlay != ovNone {
		t.Errorf("custom: range = %v, overlay = %d", m.sessionRange, m.overlay)
	}

	// A range that can't be read is reported and changes nothing.
	press(m, "T")
	m.input.SetValue("soon")
	press(m, "enter")
	if m.sessionRange != 14*day || !m.flashErr {
		t.Errorf("bad range: range = %v, flashErr = %v", m.sessionRange, m.flashErr)
	}

	press(m, "T")
	m.input.SetValue("1d")
	press(m, "esc")
	if m.sessionRange != 14*day {
		t.Errorf("esc: range = %v", m.sessionRange)
	}
}

func TestRangeRemembered(t *testing.T) {
	m := fixture(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.switchTab(tabClaude)
	press(m, "T")
	m.input.SetValue("3mo")
	press(m, "enter")

	if again := newModel(m.st, Hooks{}); again.sessionRange != 90*day {
		t.Errorf("after restart: range = %v, want 90d", again.sessionRange)
	}
	for _, bad := range []string{"not json", `{"session_range":"soon"}`} {
		if err := os.WriteFile(m.settingsPath(), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if again := newModel(m.st, Hooks{}); again.sessionRange != defaultRange {
			t.Errorf("settings %q: range = %v, want the default", bad, again.sessionRange)
		}
	}
}

// The scan must use the range, and an answer to an older range must not stick.
func TestRangeReachesScanner(t *testing.T) {
	// Point the scanners at an empty home: this test really scans.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))

	logs := filepath.Join(agy.DataDir(), "brain", "old-session", ".system_generated", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(logs, "transcript_full.jsonl")
	line := `{"step_index":0,"type":"USER_INPUT","created_at":"2026-01-01T00:00:00Z","content":"<USER_REQUEST>hi</USER_REQUEST>"}` + "\n"
	if err := os.WriteFile(transcript, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-20 * day)
	if err := os.Chtimes(transcript, old, old); err != nil {
		t.Fatal(err)
	}

	m := fixture(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.switchTab(tabAgy)
	msg := m.load(false)().(dataMsg)
	if msg.sessionRange != defaultRange || len(msg.sessions) != 0 {
		t.Errorf("7d: range %v, %d sessions, want none", msg.sessionRange, len(msg.sessions))
	}

	m.loading = true // a scan with the old range is in flight
	press(m, "t")
	if !m.loading {
		t.Error("changing range while loading started a second scan")
	}
	_, cmd := m.Update(msg)
	if cmd == nil {
		t.Fatal("stale data did not start a new scan")
	}
	if m.scannedRange == m.sessionRange {
		t.Error("stale data counted as scanned with the new range")
	}
	msg = cmd().(dataMsg)
	if msg.sessionRange != 30*day || len(msg.sessions) != 1 {
		t.Errorf("30d: range %v, %d sessions, want 1", msg.sessionRange, len(msg.sessions))
	}
	m.Update(msg)
	if m.scannedRange != m.sessionRange {
		t.Errorf("scanned %v, range %v", m.scannedRange, m.sessionRange)
	}
}
