//go:build windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const jobObjectTerminate = 0x0008

var procOpenJobObjectW = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW")

func jobName(runID string) string { return `Local\agentbox-` + runID }

func shellCommand(script string) (*exec.Cmd, []string) {
	// Agents on Windows (Claude Code included) usually speak Git Bash, so use
	// it when we were launched from one. Skip System32's bash, which is WSL.
	if os.Getenv("MSYSTEM") != "" || os.Getenv("SHELL") != "" {
		if p, err := exec.LookPath("bash"); err == nil &&
			!strings.Contains(strings.ToLower(filepath.Dir(p)), `\windows\system32`) {
			return exec.Command(p, "-c", script), []string{"bash", "-c"}
		}
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c "` + script + `"`}
	return cmd, []string{"cmd", "/c"}
}

func prepareChild(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Its own process group so CTRL_BREAK reaches only this tree.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// StartDetached starts cmd so it survives the caller exiting or having its
// process tree killed: no console window, its own process group, and out of
// the caller's job object when the job allows breakaway.
func StartDetached(cmd *exec.Cmd) error {
	base := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: base | windows.CREATE_BREAKAWAY_FROM_JOB}
	err := cmd.Start()
	if err != nil && errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// The caller's job forbids breakaway; start inside it instead.
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: base}
		cmd.Process = nil
		err = cmd.Start()
	}
	return err
}

type winGroup struct {
	pid int
	job windows.Handle
}

// NewGroup puts a started process into a named job object, so the tree can
// be killed from this process or, by name, from another agentbox process.
func NewGroup(runID string, p *os.Process) (Group, error) {
	g := &winGroup{pid: p.Pid}
	name, _ := windows.UTF16PtrFromString(jobName(runID))
	job, err := windows.CreateJobObject(nil, name)
	if err != nil {
		return g, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return g, err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return g, err
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return g, err
	}
	g.job = job
	return g, nil
}

func (g *winGroup) Interrupt() error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(g.pid))
}

func (g *winGroup) Kill() error {
	if g.job != 0 {
		if err := windows.TerminateJobObject(g.job, 1); err == nil {
			return nil
		}
	}
	return taskkill(g.pid)
}

func (g *winGroup) Close() {
	if g.job != 0 {
		windows.CloseHandle(g.job)
		g.job = 0
	}
}

// KillTree kills a run's process tree from outside its supervisor. Used when
// the supervisor is gone.
func KillTree(runID string, pid int) error {
	name, _ := windows.UTF16PtrFromString(jobName(runID))
	r, _, _ := procOpenJobObjectW.Call(jobObjectTerminate, 0, uintptr(unsafe.Pointer(name)))
	if r != 0 {
		job := windows.Handle(r)
		defer windows.CloseHandle(job)
		if err := windows.TerminateJobObject(job, 1); err == nil {
			return nil
		}
	}
	if pid <= 0 {
		return errors.New("no pid to kill")
	}
	return taskkill(pid)
}

func taskkill(pid int) error {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd.Run()
}

func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
