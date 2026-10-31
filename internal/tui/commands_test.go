package tui

import (
	"testing"

	"agentbox/internal/store"
)

func TestParseAgentbox(t *testing.T) {
	cases := []struct {
		cmd  string
		want *abCall
	}{
		{`agentbox run -d --name build --title "Build the web app" -- "npm run build"`,
			&abCall{sub: "run", name: "build", title: "Build the web app", cmd: "npm run build"}},
		{`cd /c/x && agentbox run -d --title='Unit tests' -t 30m -- go test ./... && echo hi`,
			&abCall{sub: "run", title: "Unit tests", cmd: "go test ./..."}},
		{`C:\Users\me\go\bin\agentbox.exe wait build --timeout 8m`, &abCall{sub: "wait", ref: "build"}},
		{`agentbox logs k3x9 -n 50 | tail -5`, &abCall{sub: "logs", ref: "k3x9"}},
		{`echo agentbox`, nil},
		{`npm test`, nil},
	}
	for _, c := range cases {
		got := parseAgentbox(c.cmd)
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Errorf("parseAgentbox(%q) = %+v, want %+v", c.cmd, got, c.want)
		}
	}
}

func TestCallLabel(t *testing.T) {
	runs := []*store.Run{{ID: "k3x9p2", Name: "dev", Title: "Dev server"}}
	for cmd, want := range map[string]string{
		`agentbox wait dev`:                     "wait · Dev server",
		`agentbox kill k3x`:                     "kill · Dev server",
		`agentbox run -d --name api -- "make"`:  "run · api",
		`agentbox run -d -- "npm run build"`:    "run · npm run build",
		`agentbox ls`:                           "ls",
		`agentbox status nope`:                  "status · nope",
		`agentbox run --title "Lint" -- eslint`: "run · Lint",
	} {
		if got := parseAgentbox(cmd).label(runs); got != want {
			t.Errorf("label(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestErrorLine(t *testing.T) {
	if got := errorLine("Exit code 2\n\n  boom: no such file\nmore"); got != "exit 2 · boom: no such file" {
		t.Errorf("got %q", got)
	}
}
