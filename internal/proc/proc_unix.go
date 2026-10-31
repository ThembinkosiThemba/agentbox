//go:build !windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func shellCommand(script string) (*exec.Cmd, []string) {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	return exec.Command(sh, "-c", script), []string{sh, "-c"}
}

func prepareChild(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Its own process group, so signals reach the whole tree.
	cmd.SysProcAttr.Setpgid = true
}

// StartDetached starts cmd in a new session so it survives the caller's
// terminal closing and signals sent to the caller's process group.
func StartDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

type unixGroup struct{ pgid int }

func NewGroup(_ string, p *os.Process) (Group, error) {
	return &unixGroup{pgid: p.Pid}, nil
}

func (g *unixGroup) Interrupt() error { return syscall.Kill(-g.pgid, syscall.SIGTERM) }
func (g *unixGroup) Kill() error      { return syscall.Kill(-g.pgid, syscall.SIGKILL) }
func (g *unixGroup) Close()           {}

// KillTree kills a run's process group from outside its supervisor. The child
// is its own group leader, so its pid is the pgid.
func KillTree(_ string, pid int) error {
	if pid <= 0 {
		return errors.New("no pid to kill")
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		return syscall.Kill(pid, syscall.SIGKILL)
	}
	return nil
}

func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
