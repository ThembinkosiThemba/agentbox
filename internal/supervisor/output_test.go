package supervisor

import (
	"bytes"
	"testing"
)

func TestPlainWriterStripsAcrossWrites(t *testing.T) {
	in := "\x1b[1m\x1b[38;2;173;127;168m▲ Next.js\x1b[39m\x1b[22m\r\n" +
		"\x1b]8;;http://x\x07link\x1b]8;;\x1b\\ ✓ ready\n"
	want := "▲ Next.js\r\nlink ✓ ready\n"

	for _, size := range []int{1, 2, 3, 7, len(in)} {
		var out bytes.Buffer
		p := &plainWriter{w: &out}
		for i := 0; i < len(in); i += size {
			chunk := []byte(in[i:min(i+size, len(in))])
			if n, err := p.Write(chunk); err != nil || n != len(chunk) {
				t.Fatalf("size %d: Write = %d, %v", size, n, err)
			}
		}
		p.Flush()
		if out.String() != want {
			t.Errorf("size %d: got %q, want %q", size, out.String(), want)
		}
	}
}

func TestPlainWriterDoesNotHoldFinishedOutput(t *testing.T) {
	var out bytes.Buffer
	p := &plainWriter{w: &out}
	p.Write([]byte("password: "))
	if out.String() != "password: " {
		t.Errorf("a prompt with no newline was held back: %q", out.String())
	}
}

func TestPlainWriterFlushesAnUnfinishedTail(t *testing.T) {
	var out bytes.Buffer
	p := &plainWriter{w: &out}
	p.Write([]byte("done\x1b["))
	p.Flush()
	if out.String() != "done" {
		t.Errorf("got %q, want %q", out.String(), "done")
	}
}
