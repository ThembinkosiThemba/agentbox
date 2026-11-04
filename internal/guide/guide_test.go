package guide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "CLAUDE.md")
	change, err := Install(path, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if change != Added {
		t.Fatalf("change = %v, want added", change)
	}
	want := Begin + "\nhello\n" + End + "\n"
	if got := read(t, path); got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestInstallReplacesBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if _, err := Install(path, "one"); err != nil {
		t.Fatal(err)
	}
	change, err := Install(path, "two")
	if err != nil {
		t.Fatal(err)
	}
	if change != Updated {
		t.Fatalf("change = %v, want updated", change)
	}
	got := read(t, path)
	if strings.Contains(got, "one") {
		t.Errorf("old content still present:\n%s", got)
	}
	if strings.Count(got, Begin) != 1 || strings.Count(got, End) != 1 {
		t.Errorf("markers duplicated:\n%s", got)
	}
	if !strings.Contains(got, "two") {
		t.Errorf("new content missing:\n%s", got)
	}
	change, err = Install(path, "two")
	if err != nil {
		t.Fatal(err)
	}
	if change != Unchanged {
		t.Errorf("repeat change = %v, want unchanged", change)
	}
}

func TestInstallAppendsToExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# Project rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(path, "hello"); err != nil {
		t.Fatal(err)
	}
	want := "# Project rules\n\n" + Begin + "\nhello\n" + End + "\n"
	if got := read(t, path); got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestInstallKeepsCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "GEMINI.md")
	if err := os.WriteFile(path, []byte("first\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(path, "hello"); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("mixed line endings:\n%q", got)
	}
	if !strings.Contains(got, Begin+"\r\nhello\r\n"+End+"\r\n") {
		t.Errorf("block = %q", got)
	}
}

func TestRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(path, "hello"); err != nil {
		t.Fatal(err)
	}
	ok, err := Remove(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("Remove reported no change")
	}
	if got := read(t, path); got != "before\n" {
		t.Errorf("file = %q, want %q", got, "before\n")
	}
	ok, err = Remove(path)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("second Remove reported a change")
	}
}

func TestRemoveMissingFile(t *testing.T) {
	ok, err := Remove(filepath.Join(t.TempDir(), "nope.md"))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("Remove reported a change for a missing file")
	}
}

func TestSelect(t *testing.T) {
	ts, err := Select([]string{"claude", "agy", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 3 {
		t.Fatalf("len = %d, want 3", len(ts))
	}
	if ts[1].Key != "antigravity" {
		t.Errorf("alias key = %q, want antigravity", ts[1].Key)
	}
	if _, err := Select([]string{"nope"}); err == nil {
		t.Error("unknown agent accepted")
	}
}
