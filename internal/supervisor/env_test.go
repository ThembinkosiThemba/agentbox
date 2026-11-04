package supervisor

import (
	"os"
	"strings"
	"testing"
)

var colorVars = []string{"NO_COLOR", "AGENTBOX_NO_COLOR", "FORCE_COLOR", "CLICOLOR_FORCE", "COLORTERM", "TERM"}

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
		"FORCE_COLOR":    "3",
		"CLICOLOR_FORCE": "1",
		"COLORTERM":      "truecolor",
		"TERM":           "xterm-256color",
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

func TestChildEnvRespectsNoColor(t *testing.T) {
	clearColorEnv(t)
	for _, key := range []string{"NO_COLOR", "AGENTBOX_NO_COLOR"} {
		os.Setenv(key, "1")
		env := envMap(childEnv())
		if _, ok := env["FORCE_COLOR"]; ok {
			t.Errorf("%s is set but childEnv still forced colour", key)
		}
		os.Unsetenv(key)
	}
}
