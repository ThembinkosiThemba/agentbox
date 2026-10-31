package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// flagSet wraps flag.FlagSet with short/long aliases and flags that may
// appear after positional arguments (`logs abc -f`), which is how people and
// agents tend to type them.
type flagSet struct {
	*flag.FlagSet
	pos []string
}

func newFlags(name string) *flagSet {
	return &flagSet{FlagSet: flag.NewFlagSet("agentbox "+name, flag.ContinueOnError)}
}

func (f *flagSet) boolVar(def bool, usage string, names ...string) *bool {
	p := new(bool)
	for _, n := range names {
		f.BoolVar(p, n, def, usage)
	}
	return p
}

func (f *flagSet) stringVar(def, usage string, names ...string) *string {
	p := new(string)
	for _, n := range names {
		f.StringVar(p, n, def, usage)
	}
	return p
}

func (f *flagSet) intVar(def int, usage string, names ...string) *int {
	p := new(int)
	for _, n := range names {
		f.IntVar(p, n, def, usage)
	}
	return p
}

func (f *flagSet) durationVar(def time.Duration, usage string, names ...string) *time.Duration {
	p := new(time.Duration)
	for _, n := range names {
		f.DurationVar(p, n, def, usage)
	}
	return p
}

// Parse accepts flags anywhere among the positional arguments.
func (f *flagSet) Parse(args []string) error {
	f.pos = nil
	for {
		if err := f.FlagSet.Parse(args); err != nil {
			return err
		}
		args = f.FlagSet.Args()
		if len(args) == 0 {
			return nil
		}
		f.pos = append(f.pos, args[0])
		args = args[1:]
	}
}

// ParseLeading stops at the first positional argument, leaving the rest
// untouched. `run` uses it so the command's own flags pass through.
func (f *flagSet) ParseLeading(args []string) error {
	if err := f.FlagSet.Parse(args); err != nil {
		return err
	}
	f.pos = f.FlagSet.Args()
	return nil
}

func (f *flagSet) Args() []string { return f.pos }
func (f *flagSet) NArg() int      { return len(f.pos) }
func (f *flagSet) Arg(i int) string {
	if i < len(f.pos) {
		return f.pos[i]
	}
	return ""
}

// table prints aligned columns. Unlike text/tabwriter it measures cells by
// display width, so colored cells line up.
func table(w io.Writer, header []string, rows [][]string) {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = ansi.StringWidth(h)
	}
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], ansi.StringWidth(c))
		}
	}
	line := func(cells []string, style func(string) string) {
		var b strings.Builder
		for i, c := range cells {
			if style != nil {
				c = style(c)
			}
			b.WriteString(c)
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-ansi.StringWidth(cells[i])+2))
			}
		}
		fmt.Fprintln(w, b.String())
	}
	line(header, dim)
	for _, r := range rows {
		line(r, nil)
	}
}
