package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/store"
)

const (
	day = 24 * time.Hour

	// allTime is the session range that hides nothing.
	allTime time.Duration = 0

	defaultRange = store.DefaultAge
)

// rangePresets are what `t` cycles through.
var rangePresets = []time.Duration{day, 3 * day, 7 * day, 30 * day, 90 * day, allTime}

// nextRange is the preset after cur. A custom range moves to the next longer
// preset, and all time wraps around to the shortest.
func nextRange(cur time.Duration) time.Duration {
	if cur != allTime {
		for _, p := range rangePresets {
			if p == allTime || p > cur {
				return p
			}
		}
	}
	return rangePresets[0]
}

var reRange = regexp.MustCompile(`^(\d+)\s*(m|min|h|d|w|mo|y)?$`)

// parseRange reads a range as typed: 45m, 12h, 14d, 2w, 3mo, 1y or all. A bare
// number is days.
func parseRange(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "all" {
		return allTime, nil
	}
	m := reRange.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("can't read range %q: try 12h, 14d, 2w, 3mo, 1y or all", s)
	}
	unit := day
	switch m[2] {
	case "m", "min":
		unit = time.Minute
	case "h":
		unit = time.Hour
	case "w":
		unit = 7 * day
	case "mo":
		unit = 30 * day
	case "y":
		unit = 365 * day
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || n <= 0 || n > int64(math.MaxInt64/unit) {
		return 0, fmt.Errorf("range %q is out of bounds: try 12h, 14d, 2w, 3mo, 1y or all", s)
	}
	return time.Duration(n) * unit, nil
}

// formatRange is the short form of a range, and is what parseRange reads back.
func formatRange(d time.Duration) string {
	switch {
	case d == allTime:
		return "all"
	case d%day == 0:
		return strconv.FormatInt(int64(d/day), 10) + "d"
	case d%time.Hour == 0:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	}
	return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
}

// rangeLabel is a range as it reads in the dashboard.
func rangeLabel(d time.Duration) string {
	if d == allTime {
		return "all time"
	}
	return "last " + formatRange(d)
}

// scanAge is the range as a scanner's maximum age.
func scanAge(d time.Duration) time.Duration {
	if d == allTime {
		return math.MaxInt64
	}
	return d
}

// dashboardSettings is what the dashboard remembers between launches. It sits
// beside the runs directory, in the agentbox home.
type dashboardSettings struct {
	SessionRange string `json:"session_range"`
}

func (m *model) settingsPath() string {
	return filepath.Join(filepath.Dir(m.st.Root()), "dashboard.json")
}

func (m *model) loadSessionRange() time.Duration {
	data, err := os.ReadFile(m.settingsPath())
	if err != nil {
		return defaultRange
	}
	var s dashboardSettings
	if json.Unmarshal(data, &s) != nil {
		return defaultRange
	}
	d, err := parseRange(s.SessionRange)
	if err != nil {
		return defaultRange
	}
	return d
}

func (m *model) saveSessionRange() error {
	data, err := json.Marshal(dashboardSettings{SessionRange: formatRange(m.sessionRange)})
	if err != nil {
		return err
	}
	return os.WriteFile(m.settingsPath(), append(data, '\n'), 0o644)
}
