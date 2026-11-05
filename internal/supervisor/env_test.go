package supervisor

import (
	"os"
	"strings"
	"testing"
)

var colorVars = []string{"NO_COLOR", "AGENTBOX_NO_COLOR", "FORCE_COLOR", "CLICOLOR_FORCE", "COLORTERM", "TERM",
	"CARGO_TERM_COLOR", "PY_COLORS", "PYTHON_COLORS", "DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION"}

// clearColorEnv removes the variables childEnv looks at, restoring them when
// the test ends.
func clearColorEnv(t *testing.T) {
	t.Helper()
	for _, k := range colorVars {
		k := k
		old, had := os.LookupEnv(k)
		os.Unsetenv(k)
		t.Cleanup(func() {
			if had {
				os.Setenv(k, old)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[strings.ToUpper(k)] = v
	}
	return m
}

func TestChildEnvForcesColor(t *testing.T) {
	clearColorEnv(t)
	env := envMap(childEnv())
	for k, want := range map[string]string{
		"FORCE_COLOR":      "3",
		"CLICOLOR_FORCE":   "1",
		"COLORTERM":        "truecolor",
		"TERM":             "xterm-256color",
		"CARGO_TERM_COLOR": "always",
		"PY_COLORS":        "1",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
}

func TestChildEnvKeepsCallerValues(t *testing.T) {
	clearColorEnv(t)
	os.Setenv("FORCE_COLOR", "1")
	os.Setenv("TERM", "screen-256color")
	env := envMap(childEnv())
	if env["FORCE_COLOR"] != "1" {
		t.Errorf("FORCE_COLOR = %q, want the caller's value", env["FORCE_COLOR"])
	}
	if env["TERM"] != "screen-256color" {
		t.Errorf("TERM = %q, want the caller's value", env["TERM"])
	}
}

func TestChildEnvRespectsAgentboxNoColor(t *testing.T) {
	clearColorEnv(t)
	os.Setenv("AGENTBOX_NO_COLOR", "1")
	if _, ok := envMap(childEnv())["FORCE_COLOR"]; ok {
		t.Error("AGENTBOX_NO_COLOR is set but childEnv still forced colour")
	}
}

// Agents set NO_COLOR for their own shell. The log keeps its colour anyway, and
// node warns when it sees NO_COLOR beside FORCE_COLOR, so it must not get through.
func TestChildEnvDropsInheritedNoColor(t *testing.T) {
	clearColorEnv(t)
	os.Setenv("NO_COLOR", "1")
	env := envMap(childEnv())
	if _, ok := env["NO_COLOR"]; ok {
		t.Error("NO_COLOR reached the child")
	}
	if env["FORCE_COLOR"] != "3" {
		t.Errorf("FORCE_COLOR = %q, want colour forced", env["FORCE_COLOR"])
	}
}
