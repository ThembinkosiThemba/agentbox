package tui

import (
	"path/filepath"
	"strings"

	"agentbox/internal/store"
)

// abCall is an agentbox invocation found inside a shell command an agent
// ran, e.g. `agentbox run -d --name build --title "Build" -- "npm run build"`.
type abCall struct {
	sub   string // run, wait, logs, kill, ...
	title string
	name  string
	ref   string // the run a wait/logs/kill/status refers to
	cmd   string // for run: the command it starts
}

// parseAgentbox finds the first agentbox call in a shell command line.
func parseAgentbox(cmd string) *abCall {
	if !strings.Contains(cmd, "agentbox") {
		return nil
	}
	toks := shellWords(cmd)
	for i := 0; i < len(toks)-1; i++ {
		base := strings.ToLower(filepath.Base(strings.ReplaceAll(toks[i], `\`, "/")))
		if base != "agentbox" && base != "agentbox.exe" {
			continue
		}
		c := &abCall{sub: toks[i+1]}
		args := toks[i+2:]
		for j := 0; j < len(args); j++ {
			a := args[j]
			if isSeparator(a) {
				break
			}
			val := func() string {
				if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(k, "-") {
					return v
				}
				if j+1 < len(args) {
					j++
					return args[j]
				}
				return ""
			}
			switch {
			case a == "--":
				if c.sub == "run" {
					rest := args[j+1:]
					for k, r := range rest {
						if isSeparator(r) {
							rest = rest[:k]
							break
						}
					}
					c.cmd = strings.Join(rest, " ")
				}
				return c
			case strings.HasPrefix(a, "--title"):
				c.title = val()
			case c.sub == "run" && (a == "-n" || strings.HasPrefix(a, "--name")):
				c.name = val()
			case a == "-n" || a == "-t" || a == "-C" || strings.HasPrefix(a, "--timeout") || strings.HasPrefix(a, "--cwd") ||
				strings.HasPrefix(a, "--lines") || strings.HasPrefix(a, "--older-than") || strings.HasPrefix(a, "--days"):
				val()
			case strings.HasPrefix(a, "-"):
			case c.ref == "" && c.sub != "run":
				c.ref = a
			case c.sub == "run" && c.cmd == "":
				c.cmd = a
			}
		}
		return c
	}
	return nil
}

func isSeparator(s string) bool {
	switch s {
	case "&&", "||", ";", "|", "&":
		return true
	}
	return false
}

// shellWords splits a command line roughly the way a POSIX shell would:
// quotes group, and the control operators && || ; | become their own words.
func shellWords(s string) []string {
	var out []string
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			out = append(out, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' || c == '"':
			inWord = true
			j := strings.IndexByte(s[i+1:], c)
			if j < 0 {
				cur.WriteString(s[i+1:])
				i = len(s)
				break
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
		case c == '\\' && i+1 < len(s) && s[i+1] != '\n':
			// Windows paths use backslashes; keep them unless they escape a quote.
			if s[i+1] == '"' || s[i+1] == '\'' {
				i++
			}
			cur.WriteByte(s[i])
			inWord = true
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		case c == '&' || c == '|' || c == ';':
			flush()
			op := string(c)
			if i+1 < len(s) && s[i+1] == c && c != ';' {
				op += string(c)
				i++
			}
			out = append(out, op)
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	flush()
	return out
}

// label is how an agentbox call reads in a list: the run's title when it is
// known, else its name, else the command.
func (c *abCall) label(runs []*store.Run) string {
	what := c.ref
	switch {
	case c.sub == "run" && c.title != "":
		what = c.title
	case c.sub == "run" && c.name != "":
		what = c.name
	case c.sub == "run":
		what = oneLine(c.cmd)
	default:
		if r := findRun(runs, c.ref); r != nil {
			what = r.DisplayName()
		}
	}
	if what == "" {
		return c.sub
	}
	return c.sub + " · " + what
}

// findRun resolves a run reference the way the CLI does: exact id, unique
// id prefix, then the newest run with that name.
func findRun(runs []*store.Run, ref string) *store.Run {
	if ref == "" {
		return nil
	}
	var prefix []*store.Run
	for _, r := range runs {
		if r.ID == ref {
			return r
		}
		if strings.HasPrefix(r.ID, ref) {
			prefix = append(prefix, r)
		}
	}
	if len(prefix) == 1 {
		return prefix[0]
	}
	var named *store.Run
	for _, r := range runs {
		if r.Name == ref && (named == nil || r.CreatedAt.After(named.CreatedAt)) {
			named = r
		}
	}
	return named
}
