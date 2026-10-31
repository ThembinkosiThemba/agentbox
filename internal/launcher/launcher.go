// Package launcher adds agentbox to the desktop's app launcher (the Start
// Menu on Windows, the applications menu on Linux) so the dashboard can be
// opened, and pinned, like any other app.
package launcher

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
)

// icon is the 256px app icon, drawn by tools/genicon. Windows doesn't need
// it: there the icon is embedded in agentbox.exe itself (see winres/).
//
//go:embed icon.png
var icon []byte

// Options say where to add (or remove) launchers.
type Options struct {
	Desktop bool // also put one on the desktop (Windows)
	Remove  bool
}

// Result lists the files written or removed, and what to do next.
type Result struct {
	Paths []string
	Hint  string
}

// Executable is the running agentbox binary, with symlinks resolved, so the
// launcher keeps working when invoked through a link on PATH.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

// Temporary reports whether exe looks like a throwaway build (go run), which
// a launcher should not point at.
func Temporary(exe string) bool {
	tmp := filepath.Clean(os.TempDir())
	return strings.HasPrefix(strings.ToLower(exe), strings.ToLower(tmp)) || strings.Contains(exe, "go-build")
}

func removeAll(paths ...string) (*Result, error) {
	res := &Result{}
	for _, p := range paths {
		err := os.Remove(p)
		if err == nil {
			res.Paths = append(res.Paths, p)
		} else if !os.IsNotExist(err) {
			return res, err
		}
	}
	return res, nil
}
