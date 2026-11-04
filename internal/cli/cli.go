// Package cli implements agentbox's command line. Every command is safe for
// an agent to call: output is plain when stdout is not a terminal, --json is
// available where structured output helps, and exit codes are meaningful.
package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-isatty"

	"agentbox/internal/agy"
	"agentbox/internal/claude"
	"agentbox/internal/guide"
	"agentbox/internal/human"
	"agentbox/internal/launcher"
	"agentbox/internal/opencode"
	"agentbox/internal/store"
	"agentbox/internal/supervisor"
	"agentbox/internal/tui"
)

// Version is set at release build time with -ldflags "-X agentbox/internal/cli.Version=..."
var Version = "dev"

// Exit codes with fixed meanings, so agents can tell outcomes apart.
const (
	ExitStillRunning = 75  // `wait --timeout` expired; the run keeps going (EX_TEMPFAIL)
	ExitRunTimeout   = 124 // the run hit its own --timeout, as with timeout(1)
	ExitKilled       = 137 // the run was stopped
	ExitInterrupted  = 130 // a foreground run was stopped with Ctrl+C
)

type command struct {
	name    string
	aliases []string
	args    string
	summary string
	run     func(st *store.Store, args []string) int
}

var commands []command

func init() {
	commands = []command{
		{"run", nil, "[flags] -- <command...>", "Run a command under agentbox (-d to detach)", cmdRun},
		{"ls", []string{"ps", "list"}, "[-a] [--json]", "List runs", cmdList},
		{"status", []string{"show"}, "<id> [--json]", "Show one run", cmdStatus},
		{"logs", []string{"log"}, "<id> [-f] [-n lines]", "Print a run's output", cmdLogs},
		{"wait", nil, "<id> [--timeout 8m] [-n lines]", "Block until a run finishes; exits with its code", cmdWait},
		{"kill", []string{"stop"}, "<id>...", "Stop runs and their whole process trees", cmdKill},
		{"restart", nil, "<id>", "Start a run's command again, detached", cmdRestart},
		{"rm", nil, "[-f] <id>...", "Delete finished runs", cmdRm},
		{"prune", nil, "[--older-than 24h]", "Delete all finished runs", cmdPrune},
		{"sessions", nil, "[id] [--json] [--days 3]", "List Claude Code, Antigravity and OpenCode sessions, or show one", cmdSessions},
		{"guide", nil, "[--install|--remove] [--project] [--agent names]", "Print or install instructions for coding agents", cmdGuide},
		{"shortcut", nil, "[--desktop] [--remove]", "Add agentbox to the Start menu / app launcher, to pin it", cmdShortcut},
		{"tui", []string{"ui"}, "", "Open the dashboard (default with no command)", cmdTUI},
		{"version", nil, "", "Print the version", func(*store.Store, []string) int { fmt.Println("agentbox", Version); return 0 }},
	}
}

func Main(args []string) int {
	if len(args) > 0 && args[0] == "__supervise" {
		return superviseMain(args[1:])
	}
	st, err := store.Open()
	if err != nil {
		return die(err)
	}
	if len(args) == 0 {
		return cmdTUI(st, nil)
	}
	name := args[0]
	if name == "help" || name == "-h" || name == "--help" {
		usage(os.Stdout)
		return 0
	}
	if name == "-v" || name == "--version" {
		name = "version"
	}
	for _, c := range commands {
		if c.name == name || contains(c.aliases, name) {
			return c.run(st, args[1:])
		}
	}
	fmt.Fprintf(os.Stderr, "agentbox: unknown command %q\n\n", name)
	usage(os.Stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "agentbox %s: see and manage what coding agents run on this machine.\n\n", Version)
	fmt.Fprintln(w, "Usage:\n  agentbox                 open the dashboard\n  agentbox <command> ...")
	fmt.Fprintln(w, "\nCommands:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range commands {
		fmt.Fprintf(tw, "  %s %s\t%s\n", c.name, c.args, c.summary)
	}
	tw.Flush()
	fmt.Fprintln(w, "\nRun ids can be shortened to any unique prefix, or given as a run's --name.")
	fmt.Fprintln(w, "Run `agentbox guide` for how agents should use agentbox.")
}

func superviseMain(args []string) int {
	if len(args) != 1 {
		return 2
	}
	st, err := store.Open()
	if err != nil {
		return 1
	}
	code, _ := supervisor.Supervise(st, args[0], false)
	return code
}

// ---- run ----

func cmdRun(st *store.Store, args []string) int {
	fs := newFlags("run")
	detach := fs.boolVar(false, "Start in the background and return immediately", "d", "detach")
	name := fs.stringVar("", "A name to find the run by", "n", "name")
	title := fs.stringVar("", "What the run is for, shown in the dashboard (e.g. \"Run e2e tests\")", "title")
	timeout := fs.durationVar(0, "Stop the run after this long (e.g. 30m)", "t", "timeout")
	cwd := fs.stringVar("", "Working directory (default: current)", "C", "cwd")
	forceShell := fs.boolVar(false, "Run through the shell even with several arguments", "s", "shell")
	noShell := fs.boolVar(false, "Exec directly even with a single argument", "no-shell")
	quiet := fs.boolVar(false, "With -d, print only the run id", "q", "quiet")
	asJSON := fs.boolVar(false, "With -d, print the run as JSON", "json")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: agentbox run [flags] -- <command...>\n\nA single argument runs through the shell (pipes, &&, globs work);\nseveral arguments are exec'd directly.\n\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.ParseLeading(args); err != nil {
		return 2
	}
	argv := fs.Args()
	if len(argv) == 0 {
		fs.Usage()
		return 2
	}
	useShell := (len(argv) == 1 || *forceShell) && !*noShell

	r, err := supervisor.Create(st, supervisor.Options{
		Name: *name, Title: *title, Argv: argv, UseShell: useShell, Cwd: *cwd, Timeout: *timeout, Detach: *detach,
	})
	if err != nil {
		return die(err)
	}

	if !*detach {
		if !*quiet {
			fmt.Fprintf(os.Stderr, "agentbox: run %s\n", r.ID)
		}
		if _, err := supervisor.Supervise(st, r.ID, true); err != nil {
			fmt.Fprintln(os.Stderr, "agentbox:", err)
		}
		if r, err = st.Load(r.ID); err != nil {
			return die(err)
		}
		if r.Status == store.StatusKilled {
			return ExitInterrupted
		}
		return exitCode(r)
	}

	r, err = supervisor.SpawnDetached(st, r)
	if err != nil {
		return die(err)
	}
	return reportStarted(r, *quiet, *asJSON)
}

func reportStarted(r *store.Run, quiet, asJSON bool) int {
	switch {
	case asJSON:
		printJSON(r)
	case quiet:
		fmt.Println(r.ID)
	default:
		if r.Status == store.StatusFailed {
			fmt.Fprintf(os.Stderr, "agentbox: run %s failed to start: %s\n", r.ID, r.Error)
			break
		}
		fmt.Printf("%s started (pid %d): %s\n", bold(r.ID), r.PID, r.DisplayName())
		fmt.Println(dim(fmt.Sprintf("  agentbox wait %[1]s · agentbox logs %[1]s -f · agentbox kill %[1]s", r.ID)))
	}
	if r.Status == store.StatusFailed {
		return exitCode(r)
	}
	return 0
}

// ---- ls / status ----

func cmdList(st *store.Store, args []string) int {
	fs := newFlags("ls")
	all := fs.boolVar(false, "Show all runs (default: running plus the 20 most recent)", "a", "all")
	asJSON := fs.boolVar(false, "Print JSON", "json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	runs, err := st.List()
	if err != nil {
		return die(err)
	}
	if !*all {
		var keep []*store.Run
		for i, r := range runs {
			if !r.Status.Terminal() || i < 20 {
				keep = append(keep, r)
			}
		}
		runs = keep
	}
	if *asJSON {
		printJSON(orEmpty(runs))
		return 0
	}
	if len(runs) == 0 {
		fmt.Println(dim("No runs yet. Start one with: agentbox run -d -- <command>"))
		return 0
	}
	now := time.Now()
	var rows [][]string
	for _, r := range runs {
		by := r.Agent
		if by == "" {
			by = "you"
		}
		rows = append(rows, []string{r.ID, statusText(r), human.Duration(r.Duration(now)),
			human.Ago(r.CreatedAt, now), by, truncate(r.DisplayName(), 60)})
	}
	table(os.Stdout, []string{"ID", "STATUS", "TIME", "STARTED", "BY", "COMMAND"}, rows)
	return 0
}

func cmdStatus(st *store.Store, args []string) int {
	fs := newFlags("status")
	asJSON := fs.boolVar(false, "Print JSON", "json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	r, err := st.Resolve(fs.Arg(0))
	if err != nil {
		return die(err)
	}
	if *asJSON {
		printJSON(r)
		return 0
	}
	now := time.Now()
	fmt.Printf("%s  %s  %s\n", bold(r.ID), statusText(r), human.Duration(r.Duration(now)))
	row := func(k, v string) {
		if v != "" {
			fmt.Printf("  %-9s %s\n", dim(k), v)
		}
	}
	row("title", r.Title)
	row("name", r.Name)
	row("command", r.CommandLine())
	row("cwd", r.Cwd)
	row("started", human.Clock(r.CreatedAt, now))
	if r.EndedAt != nil {
		row("ended", human.Clock(*r.EndedAt, now))
	}
	row("exit", r.ExitText())
	if r.PID > 0 {
		row("pid", fmt.Sprint(r.PID))
	}
	if r.Agent != "" {
		row("agent", strings.TrimSpace(r.Agent+" "+r.SessionID))
	}
	if r.TimeoutSec > 0 {
		row("timeout", human.Duration(time.Duration(r.TimeoutSec)*time.Second))
	}
	row("error", r.Error)
	row("log", st.LogPath(r.ID))
	return 0
}

// ---- logs / wait ----

func cmdLogs(st *store.Store, args []string) int {
	fs := newFlags("logs")
	follow := fs.boolVar(false, "Keep printing output until the run ends", "f", "follow")
	n := fs.intVar(0, "Print only the last N lines", "n", "lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	r, err := st.Resolve(fs.Arg(0))
	if err != nil {
		return die(err)
	}
	path := st.LogPath(r.ID)
	var offset int64
	if *n > 0 {
		lines, _, err := store.Tail(path, *n, 4<<20)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return die(err)
		}
		for _, l := range lines {
			printLogLine(l)
		}
		if fi, err := os.Stat(path); err == nil {
			offset = fi.Size()
		}
	} else {
		offset = copyFrom(path, 0)
	}
	if !*follow {
		return 0
	}
	for {
		cur, err := st.Load(r.ID)
		offset = copyFrom(path, offset)
		if err != nil || cur.Status.Terminal() {
			copyFrom(path, offset)
			return 0
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func copyFrom(path string, offset int64) int64 {
	f, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	if stdoutIsTTY() {
		n, _ := io.Copy(os.Stdout, f)
		return offset + n
	}
	// The log carries the command's colours; strip them when our own output
	// is piped, where the CLI promises plain text.
	br := bufio.NewReader(f)
	var n int64
	for {
		line, err := br.ReadString('\n')
		n += int64(len(line))
		if line != "" {
			fmt.Print(ansi.Strip(line))
		}
		if err != nil {
			return offset + n
		}
	}
}

// printLogLine prints one line of a run's output, plainly when stdout is
// not a terminal.
func printLogLine(line string) {
	if !stdoutIsTTY() {
		line = ansi.Strip(line)
	}
	fmt.Println(line)
}

func stdoutIsTTY() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

func cmdWait(st *store.Store, args []string) int {
	fs := newFlags("wait")
	timeout := fs.durationVar(0, "Give up after this long and exit 75; the run keeps going", "t", "timeout")
	n := fs.intVar(20, "Print the last N lines of output when the run ends (0 for none)", "n", "lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	r, err := st.Resolve(fs.Arg(0))
	if err != nil {
		return die(err)
	}
	start := time.Now()
	for !r.Status.Terminal() {
		if *timeout > 0 && time.Since(start) > *timeout {
			fmt.Fprintf(os.Stderr, "agentbox: %s still running after %s. It keeps going; run `agentbox wait %s` again.\n",
				r.ID, human.Duration(r.Duration(time.Now())), r.ID)
			return ExitStillRunning
		}
		time.Sleep(250 * time.Millisecond)
		if r, err = st.Load(r.ID); err != nil {
			return die(err)
		}
	}
	if *n > 0 {
		lines, truncated, _ := store.Tail(st.LogPath(r.ID), *n, 1<<20)
		if truncated {
			fmt.Println(dim(fmt.Sprintf("… (last %d lines; agentbox logs %s for all)", len(lines), r.ID)))
		}
		for _, l := range lines {
			printLogLine(l)
		}
	}
	fmt.Fprintf(os.Stderr, "agentbox: %s %s after %s\n", r.ID, statusText(r), human.Duration(r.Duration(time.Now())))
	return exitCode(r)
}

// ---- kill / restart / rm / prune ----

func cmdKill(st *store.Store, args []string) int {
	if len(args) == 0 {
		return die(errors.New("usage: agentbox kill <id>..."))
	}
	rc := 0
	for _, ref := range args {
		r, err := st.Resolve(ref)
		if err != nil {
			fmt.Fprintln(os.Stderr, "agentbox:", err)
			rc = 1
			continue
		}
		if r.Status.Terminal() && r.Status != store.StatusLost {
			fmt.Printf("%s already %s\n", r.ID, r.Status)
			continue
		}
		r, err = supervisor.Kill(st, r)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agentbox: kill %s: %v\n", r.ID, err)
			rc = 1
			continue
		}
		fmt.Printf("%s %s\n", r.ID, statusText(r))
	}
	return rc
}

func cmdRestart(st *store.Store, args []string) int {
	fs := newFlags("restart")
	quiet := fs.boolVar(false, "Print only the new run id", "q", "quiet")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	old, err := st.Resolve(fs.Arg(0))
	if err != nil {
		return die(err)
	}
	r, err := Restart(st, old)
	if err != nil {
		return die(err)
	}
	return reportStarted(r, *quiet, false)
}

// Restart stops old if needed and starts its command again as a new
// detached run.
func Restart(st *store.Store, old *store.Run) (*store.Run, error) {
	if !old.Status.Terminal() || old.Status == store.StatusLost {
		if _, err := supervisor.Kill(st, old); err != nil {
			return nil, err
		}
	}
	r, err := supervisor.Create(st, supervisor.Options{
		Name: old.Name, Title: old.Title, Argv: old.Command, UseShell: old.UseShell, Cwd: old.Cwd,
		Timeout: time.Duration(old.TimeoutSec) * time.Second, Detach: true, RestartOf: old.ID,
	})
	if err != nil {
		return nil, err
	}
	return supervisor.SpawnDetached(st, r)
}

func cmdRm(st *store.Store, args []string) int {
	fs := newFlags("rm")
	force := fs.boolVar(false, "Kill running runs first", "f", "force")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		return die(errors.New("usage: agentbox rm [-f] <id>..."))
	}
	rc := 0
	for _, ref := range fs.Args() {
		r, err := st.Resolve(ref)
		if err == nil && !r.Status.Terminal() {
			if !*force {
				err = fmt.Errorf("%s is %s; stop it first or use -f", r.ID, r.Status)
			} else {
				_, err = supervisor.Kill(st, r)
			}
		}
		if err == nil {
			err = st.Delete(r.ID)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "agentbox:", err)
			rc = 1
			continue
		}
		fmt.Println("removed", r.ID)
	}
	return rc
}

func cmdPrune(st *store.Store, args []string) int {
	fs := newFlags("prune")
	older := fs.durationVar(0, "Only runs that finished at least this long ago", "older-than")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	runs, err := st.List()
	if err != nil {
		return die(err)
	}
	n := 0
	for _, r := range runs {
		if !r.Status.Terminal() || r.Status == store.StatusLost {
			continue
		}
		if r.EndedAt != nil && time.Since(*r.EndedAt) < *older {
			continue
		}
		if st.Delete(r.ID) == nil {
			n++
		}
	}
	fmt.Printf("removed %d finished runs\n", n)
	return 0
}

// ---- sessions / guide / tui ----

func cmdSessions(_ *store.Store, args []string) int {
	fs := newFlags("sessions")
	asJSON := fs.boolVar(false, "Print JSON", "json")
	days := fs.intVar(3, "Include sessions active within this many days (0 for all)", "days")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	age := time.Duration(*days) * 24 * time.Hour
	if *days <= 0 {
		age = math.MaxInt64
	}
	sessions, err := claude.NewScanner().Scan(age, 0)
	if err != nil {
		return die(err)
	}
	agySessions, err := agy.NewScanner().Scan(age, 0)
	if err != nil {
		return die(err)
	}
	sessions = append(sessions, agySessions...)
	ocScanner := opencode.NewScanner()
	ocSessions, err := ocScanner.Scan(age, 0)
	ocScanner.Close()
	if err != nil {
		return die(err)
	}
	sessions = append(sessions, ocSessions...)
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].ModTime.After(sessions[j].ModTime) })
	now := time.Now()
	if ref := fs.Arg(0); ref != "" {
		for _, s := range sessions {
			if strings.HasPrefix(s.ID, ref) {
				return printSession(s, now)
			}
		}
		return die(fmt.Errorf("no session %q in the last %d days", ref, *days))
	}
	if *asJSON {
		type row struct {
			Agent, ID, Title, Cwd, Branch string
			Model                         string
			Active                        bool
			LastActivity                  time.Time
			Prompts, Commands, Tasks      int
			RunningTasks                  int
			CostUSD                       float64
		}
		rows := []row{}
		for _, s := range sessions {
			rows = append(rows, row{s.Agent, s.ID, s.DisplayTitle(), s.Cwd, s.Branch, s.Model, s.Active(now), s.LastActivity,
				s.Prompts, len(s.Execs), len(s.Tasks), s.RunningTasks(now), s.CostUSD})
		}
		printJSON(rows)
		return 0
	}
	if len(sessions) == 0 {
		fmt.Println(dim("No agent sessions found in " + claude.ConfigDir() + ", " + agy.DataDir() + " or " + opencode.DataDir()))
		return 0
	}
	var rows [][]string
	for _, s := range sessions {
		mark := " "
		if s.Active(now) {
			mark = green("●")
		}
		tasks := fmt.Sprint(len(s.Tasks))
		if r := s.RunningTasks(now); r > 0 {
			tasks = fmt.Sprintf("%d (%d running)", len(s.Tasks), r)
		}
		rows = append(rows, []string{mark, s.ID[:8], s.Agent, human.Ago(s.ModTime, now), fmt.Sprint(len(s.Execs)),
			tasks, truncate(baseName(s.Cwd), 24), truncate(s.DisplayTitle(), 50)})
	}
	table(os.Stdout, []string{"", "SESSION", "AGENT", "LAST", "CMDS", "TASKS", "PROJECT", "TITLE"}, rows)
	return 0
}

func printSession(s *claude.Session, now time.Time) int {
	state := "idle"
	if s.Active(now) {
		state = green("active")
	}
	fmt.Printf("%s  %s  %s\n", bold(s.ID), state, s.DisplayTitle())
	fmt.Printf("  %s %s   %s %s   %s %s\n", dim("agent"), s.Agent, dim("project"), s.Cwd, dim("last"), human.Ago(s.ModTime, now))
	if len(s.Tasks) > 0 {
		fmt.Println("\n" + dim("BACKGROUND TASKS"))
		var rows [][]string
		for _, t := range s.Tasks {
			desc := t.Description
			if desc == "" {
				desc = claude.ShortCommand(t.Command)
			}
			rows = append(rows, []string{t.ID, string(t.State(s, now)), human.Duration(t.Duration(s, now)), truncate(desc, 70)})
		}
		table(os.Stdout, []string{"ID", "STATE", "TIME", "TASK"}, rows)
	}
	if len(s.Execs) > 0 {
		fmt.Println("\n" + dim("RECENT COMMANDS"))
		var rows [][]string
		for i := max(len(s.Execs)-15, 0); i < len(s.Execs); i++ {
			e := s.Execs[i]
			result := "ok"
			switch {
			case !e.Done():
				result = yellow("running")
			case e.Interrupted:
				result = "interrupted"
			case e.IsError:
				result = red("error")
			case e.TaskID != "":
				result = "bg " + e.TaskID
			}
			rows = append(rows, []string{human.Clock(e.Start, now), result, human.Duration(e.Duration(now)), truncate(claude.ShortCommand(e.Command), 70)})
		}
		table(os.Stdout, []string{"AT", "RESULT", "TIME", "COMMAND"}, rows)
	}
	return 0
}

func cmdGuide(_ *store.Store, args []string) int {
	fs := newFlags("guide")
	install := fs.boolVar(false, "Write the guide into your agents' instruction files", "install")
	remove := fs.boolVar(false, "Remove the guide from your agents' instruction files", "remove")
	project := fs.boolVar(false, "Use files in the current project instead of user-wide files", "project")
	all := fs.boolVar(false, "Include every supported agent, not only those found on this machine", "all")
	agent := fs.stringsVar("Install for these agents only (claude, antigravity, opencode, codex); repeatable", "agent")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*install && !*remove {
		fmt.Print(guide.Content)
		return 0
	}
	if *install && *remove {
		return die(errors.New("use either --install or --remove, not both"))
	}

	var targets []guide.Target
	var err error
	switch {
	case len(*agent) > 0:
		targets, err = guide.Select(splitList(*agent))
		if err != nil {
			return die(err)
		}
	case *all || *remove:
		targets = guide.Targets()
	default:
		targets = guide.Detected()
		if len(targets) == 0 {
			return die(errors.New("no supported coding agents found; name one with --agent claude, antigravity, opencode or codex"))
		}
	}

	cwd, _ := os.Getwd()
	seen := make(map[string]bool)
	changed := 0
	for _, t := range targets {
		path := t.Global
		if *project {
			path = filepath.Join(cwd, t.Project)
		}
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if *remove {
			ok, err := guide.Remove(path)
			if err != nil {
				fmt.Fprintln(os.Stderr, "agentbox:", err)
				return 1
			}
			if ok {
				fmt.Println("removed", displayPath(path))
				changed++
			}
			continue
		}
		change, err := guide.Install(path, guide.Content)
		if err != nil {
			fmt.Fprintln(os.Stderr, "agentbox:", err)
			return 1
		}
		if change != guide.Unchanged {
			fmt.Println(change, displayPath(path))
			changed++
		}
	}
	if changed == 0 {
		if *remove {
			fmt.Println("nothing to remove")
		} else {
			fmt.Println("already up to date")
		}
	}
	return 0
}

func cmdShortcut(_ *store.Store, args []string) int {
	fs := newFlags("shortcut")
	desktop := fs.boolVar(false, "Also add one to the desktop (Windows)", "desktop")
	remove := fs.boolVar(false, "Remove the shortcuts instead", "remove")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	exe, err := launcher.Executable()
	if err != nil {
		return die(err)
	}
	if !*remove && launcher.Temporary(exe) {
		return die(fmt.Errorf("%s looks like a temporary build; install agentbox first (go install . or the installer)", exe))
	}
	res, err := launcher.Install(exe, launcher.Options{Desktop: *desktop, Remove: *remove})
	if err != nil {
		return die(err)
	}
	verb := "added"
	if *remove {
		verb = "removed"
	}
	if len(res.Paths) == 0 {
		fmt.Println("nothing to remove")
	}
	for _, p := range res.Paths {
		fmt.Println(verb, p)
	}
	if res.Hint != "" {
		fmt.Println(dim(res.Hint))
	}
	return 0
}

func cmdTUI(st *store.Store, _ []string) int {
	if err := tui.Run(st, tui.Hooks{Restart: Restart, Version: Version}); err != nil {
		return die(err)
	}
	return 0
}

// ---- helpers ----

// exitCode maps a finished run to the exit code agentbox reports. Raw codes
// of stopped processes are never passed through: on Windows they are NTSTATUS
// values like 0xC000013A, which shells read as "interrupted" and abort on.
func exitCode(r *store.Run) int {
	switch r.Status {
	case store.StatusSucceeded:
		return 0
	case store.StatusTimedOut:
		return ExitRunTimeout
	case store.StatusKilled:
		return ExitKilled
	}
	if r.ExitCode != nil && *r.ExitCode > 0 && *r.ExitCode < 256 {
		return *r.ExitCode
	}
	return 1
}

func statusText(r *store.Run) string {
	s := string(r.Status)
	if x := r.ExitText(); r.Status == store.StatusFailed && x != "" {
		s = "failed (" + x + ")"
	}
	switch r.Status {
	case store.StatusRunning, store.StatusPending:
		return yellow(s)
	case store.StatusSucceeded:
		return green(s)
	case store.StatusFailed, store.StatusTimedOut, store.StatusLost:
		return red(s)
	}
	return dim(s)
}

var (
	styleBold   = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

func bold(s string) string   { return styleBold.Render(s) }
func dim(s string) string    { return styleDim.Render(s) }
func green(s string) string  { return styleGreen.Render(s) }
func yellow(s string) string { return styleYellow.Render(s) }
func red(s string) string    { return styleRed.Render(s) }

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func orEmpty(runs []*store.Run) []*store.Run {
	if runs == nil {
		return []*store.Run{}
	}
	return runs
}

func die(err error) int {
	fmt.Fprintln(os.Stderr, "agentbox:", err)
	return 1
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func baseName(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// displayPath shortens a path under the home directory or the working
// directory to ~ or a relative path, for messages.
func displayPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return "~" + string(os.PathSeparator) + rel
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return rel
		}
	}
	return path
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
