// Package supervisor starts and watches runs. A supervisor owns exactly one
// run: it spawns the child, streams its output to the run's log, heartbeats,
// honours kill requests and timeouts, and records the outcome.
package supervisor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"

	"agentbox/internal/proc"
	"agentbox/internal/store"
)

// KillGrace is how long a run gets between the polite stop signal and a
// hard kill of its whole process tree.
const KillGrace = 5 * time.Second

type Options struct {
	Name      string
	Argv      []string
	UseShell  bool
	Cwd       string
	Timeout   time.Duration
	Detach    bool
	RestartOf string
}

// Create records a new pending run, attributing it to the calling agent when
// one can be detected from the environment.
func Create(st *store.Store, o Options) (*store.Run, error) {
	if len(o.Argv) == 0 {
		return nil, errors.New("no command given")
	}
	cwd := o.Cwd
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return nil, err
		}
	}
	r := &store.Run{
		Name:       o.Name,
		Command:    o.Argv,
		UseShell:   o.UseShell,
		Cwd:        cwd,
		Detached:   o.Detach,
		TimeoutSec: int64(o.Timeout / time.Second),
		RestartOf:  o.RestartOf,
	}
	attribute(r)
	if err := st.Create(r); err != nil {
		return nil, err
	}
	return r, nil
}

func attribute(r *store.Run) {
	switch {
	case os.Getenv("AGENTBOX_AGENT") != "":
		r.Agent = os.Getenv("AGENTBOX_AGENT")
	case os.Getenv("CLAUDECODE") != "" || os.Getenv("CLAUDE_CODE_SESSION_ID") != "":
		r.Agent = "claude-code"
		r.SessionID = os.Getenv("CLAUDE_CODE_SESSION_ID")
		r.AgentPID, _ = strconv.Atoi(os.Getenv("CLAUDE_PID"))
	case os.Getenv("AI_AGENT") != "":
		r.Agent, _, _ = strings.Cut(os.Getenv("AI_AGENT"), "_")
	}
}

// SpawnDetached starts a background supervisor for r and waits briefly for
// it to report the child as started, so launch errors surface to the caller.
func SpawnDetached(st *store.Store, r *store.Run) (*store.Run, error) {
	exe, err := os.Executable()
	if err != nil {
		return r, err
	}
	cmd := exec.Command(exe, "__supervise", r.ID)
	cmd.Dir = r.Cwd
	if err := proc.StartDetached(cmd); err != nil {
		r.Status, r.Error = store.StatusFailed, "start supervisor: "+err.Error()
		_ = st.Save(r)
		return r, err
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		cur, err := st.Load(r.ID)
		if err == nil && cur.Status != store.StatusPending {
			return cur, nil
		}
	}
	return st.Load(r.ID)
}

// Supervise runs the child for run id to completion and returns its exit
// code. In the foreground, output is also streamed to this process's stdio
// and Ctrl+C stops the run.
func Supervise(st *store.Store, id string, foreground bool) (int, error) {
	r, err := st.Load(id)
	if err != nil {
		return 1, err
	}
	// Not O_APPEND: on Windows that yields an append-only handle, and some
	// children (MSYS/Git Bash among them) silently fail to write through it.
	logf, err := os.OpenFile(st.LogPath(id), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fail(st, r, "open log: "+err.Error())
	}
	defer logf.Close()
	if _, err := logf.Seek(0, io.SeekEnd); err != nil {
		return fail(st, r, "open log: "+err.Error())
	}

	cmd, shell := proc.Command(r.Command, r.UseShell)
	cmd.Dir = r.Cwd
	cmd.Env = append(os.Environ(), "AGENTBOX_RUN_ID="+r.ID)
	cmd.WaitDelay = 2 * time.Second
	if foreground {
		// Piped input is forwarded. A terminal is not: the child sits in its
		// own process group and would be stopped by SIGTTIN on read.
		if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
			cmd.Stdin = os.Stdin
		}
		cmd.Stdout = io.MultiWriter(logf, os.Stdout)
		cmd.Stderr = io.MultiWriter(logf, os.Stderr)
	} else {
		cmd.Stdout, cmd.Stderr = logf, logf
	}

	r.Shell = shell
	r.SupervisorPID = os.Getpid()
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(logf, "agentbox: failed to start: %v\n", err)
		return fail(st, r, err.Error())
	}
	group, gerr := proc.NewGroup(r.ID, cmd.Process)
	defer group.Close()

	now := time.Now()
	r.PID, r.StartedAt, r.Status = cmd.Process.Pid, &now, store.StatusRunning
	if gerr != nil {
		r.Error = "process group: " + gerr.Error()
	}
	_ = st.Save(r)
	_ = st.Heartbeat(r.ID)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var interrupts chan os.Signal
	if foreground {
		interrupts = make(chan os.Signal, 1)
		signal.Notify(interrupts, os.Interrupt)
		defer signal.Stop(interrupts)
	}

	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	lastBeat := time.Now()
	var stopReason store.Status
	var hardKillAt time.Time
	stop := func(reason store.Status) {
		if stopReason != "" {
			return
		}
		stopReason = reason
		hardKillAt = time.Now().Add(KillGrace)
		if err := group.Interrupt(); err != nil {
			_ = group.Kill()
		}
	}

	var waitErr error
loop:
	for {
		select {
		case waitErr = <-done:
			break loop
		case <-interrupts:
			stop(store.StatusKilled)
		case <-tick.C:
			if time.Since(lastBeat) >= store.HeartbeatInterval {
				_ = st.Heartbeat(r.ID)
				lastBeat = time.Now()
			}
			if st.KillRequested(r.ID) {
				stop(store.StatusKilled)
			}
			if r.TimeoutSec > 0 && time.Since(*r.StartedAt) > time.Duration(r.TimeoutSec)*time.Second {
				stop(store.StatusTimedOut)
			}
			if stopReason != "" && time.Now().After(hardKillAt) {
				_ = group.Kill()
			}
		}
	}
	// The run is over once its main process exits; take any stragglers it
	// left behind down with it.
	_ = group.Kill()

	code := cmd.ProcessState.ExitCode()
	end := time.Now()
	r.EndedAt, r.ExitCode = &end, &code
	switch {
	case stopReason != "":
		r.Status = stopReason
	case code == 0:
		r.Status = store.StatusSucceeded
	default:
		r.Status = store.StatusFailed
		var ee *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &ee) {
			r.Error = waitErr.Error()
		}
	}
	if err := st.Save(r); err != nil {
		return code, err
	}
	_ = st.Heartbeat(r.ID)
	return code, nil
}

func fail(st *store.Store, r *store.Run, msg string) (int, error) {
	end := time.Now()
	code := 127
	r.Status, r.Error, r.EndedAt, r.ExitCode = store.StatusFailed, msg, &end, &code
	if r.StartedAt == nil {
		r.StartedAt = &end
	}
	_ = st.Save(r)
	return code, errors.New(msg)
}

// Kill stops a run. It asks the supervisor first and falls back to killing
// the process tree directly when the supervisor is gone. It returns the run's
// final state.
func Kill(st *store.Store, r *store.Run) (*store.Run, error) {
	if r.Status.Terminal() && r.Status != store.StatusLost {
		return r, nil
	}
	if err := st.RequestKill(r.ID); err != nil {
		return r, err
	}
	if r.Status != store.StatusLost {
		deadline := time.Now().Add(KillGrace + 3*time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			cur, err := st.Load(r.ID)
			if err != nil {
				return r, err
			}
			if cur.Status.Terminal() && cur.Status != store.StatusLost {
				return cur, nil
			}
			if cur.Status == store.StatusLost {
				break
			}
		}
	}
	// Supervisor is not answering: kill the tree ourselves and close the run.
	err := proc.KillTree(r.ID, r.PID)
	if err != nil && proc.Alive(r.PID) {
		return r, err
	}
	end := time.Now()
	r.Status, r.EndedAt = store.StatusKilled, &end
	if r.Error == "" {
		r.Error = "supervisor was not responding; killed directly"
	}
	return r, st.Save(r)
}
