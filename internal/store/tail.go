package store

import (
	"bytes"
	"io"
	"os"
)

// Tail returns up to the last n lines of the file at path (all lines when n
// <= 0), reading at most maxBytes from its end. The second value reports
// whether earlier content was skipped.
func Tail(path string, n int, maxBytes int64) ([]string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	start := int64(0)
	if fi.Size() > maxBytes {
		start = fi.Size() - maxBytes
	}
	buf := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return nil, false, err
	}
	truncated := start > 0
	if truncated {
		// Drop the partial first line.
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	buf = bytes.TrimRight(buf, "\n")
	if len(buf) == 0 {
		return nil, truncated, nil
	}
	lines := bytes.Split(buf, []byte{'\n'})
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
		truncated = true
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(bytes.TrimRight(l, "\r"))
	}
	return out, truncated, nil
}
