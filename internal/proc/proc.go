// Package proc holds the platform-specific process handling: launching a
// supervisor that outlives its caller, putting a child and everything it
// spawns into one killable unit, and checking whether a pid is alive.
package proc

import (
	"os"
	"os/exec"
	"strings"
)

// Command builds the exec.Cmd for argv. With useShell, argv is joined into a
// script and handed to the platform shell. It returns the shell prefix used
// (empty when exec'd directly) so it can be recorded and shown.
func Command(argv []string, useShell bool) (*exec.Cmd, []string) {
	if !useShell {
		cmd := exec.Command(argv[0], argv[1:]...)
		prepareChild(cmd)
		return cmd, nil
	}
	script := strings.Join(argv, " ")
	if custom := strings.Fields(os.Getenv("AGENTBOX_SHELL")); len(custom) > 0 {
		cmd := exec.Command(custom[0], append(custom[1:], script)...)
		prepareChild(cmd)
		return cmd, custom
	}
	cmd, prefix := shellCommand(script)
	prepareChild(cmd)
	return cmd, prefix
}

// Group is a child process and all of its descendants.
type Group interface {
	// Interrupt asks the tree to stop (SIGTERM / CTRL_BREAK).
	Interrupt() error
	// Kill stops the whole tree immediately.
	Kill() error
	Close()
}
