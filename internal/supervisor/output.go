package supervisor

import (
	"bytes"
	"io"
	"os"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-isatty"
)

// ShowColor reports whether a run's colours should reach f: it has to be a
// terminal, and the user must not have asked for plain output.
func ShowColor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("AGENTBOX_NO_COLOR") != "" {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// plainWriter removes escape sequences from a stream. A sequence or rune split
// across writes is held back until the rest of it arrives.
type plainWriter struct {
	w    io.Writer
	held []byte
}

func (p *plainWriter) Write(b []byte) (int, error) {
	data := append(p.held, b...)
	cut := completeLen(data)
	p.held = bytes.Clone(data[cut:])
	if _, err := io.WriteString(p.w, ansi.Strip(string(data[:cut]))); err != nil {
		return 0, err
	}
	return len(b), nil
}

// Flush writes whatever was held back once the stream has ended.
func (p *plainWriter) Flush() {
	_, _ = io.WriteString(p.w, ansi.Strip(string(p.held)))
	p.held = nil
}

// completeLen is how much of b can be stripped now, leaving out a trailing
// escape sequence or rune that isn't finished.
func completeLen(b []byte) int {
	if i := bytes.LastIndexByte(b, 0x1b); i >= 0 && unfinished(b[i+1:]) {
		return i
	}
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return i
			}
			break
		}
	}
	return len(b)
}

// unfinished reports whether seq, the bytes after an ESC, still needs more.
func unfinished(seq []byte) bool {
	if len(seq) == 0 {
		return true
	}
	switch seq[0] {
	case '[': // CSI ends at its final byte
		for _, c := range seq[1:] {
			if c >= 0x40 && c <= 0x7e {
				return false
			}
		}
		return true
	case ']': // OSC ends at BEL, or at ST, which starts a later ESC
		return bytes.IndexByte(seq, 0x07) < 0
	}
	return false
}
