//go:build !windows

package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// Install adds agentbox to the applications menu (Linux, freedesktop) or
// writes a .command file that opens it in Terminal (macOS), or removes them.
func Install(exe string, o Options) (*Result, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	switch runtime.GOOS {
	case "darwin":
		return installMac(home, exe, o)
	case "windows", "plan9", "js", "wasip1":
		return nil, fmt.Errorf("launchers are not supported on %s", runtime.GOOS)
	}
	return installXDG(home, exe, o)
}

func installXDG(home, exe string, o Options) (*Result, error) {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	entry := filepath.Join(data, "applications", "agentbox.desktop")
	iconPath := filepath.Join(data, "icons", "hicolor", "256x256", "apps", "agentbox.png")
	if o.Remove {
		return removeAll(entry, iconPath)
	}
	if err := writeFile(iconPath, icon, 0o644); err != nil {
		return nil, err
	}
	desktop := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=agentbox\n" +
		"Comment=See and manage what your coding agents run\n" +
		"Exec=" + strconv.Quote(exe) + "\n" +
		"Icon=" + iconPath + "\n" +
		"Terminal=true\n" +
		"Categories=Development;Utility;\n" +
		"Keywords=agent;claude;terminal;jobs;\n"
	if err := writeFile(entry, []byte(desktop), 0o755); err != nil {
		return nil, err
	}
	return &Result{
		Paths: []string{entry, iconPath},
		Hint:  "agentbox is in your applications menu; add it to your dock or panel from there.",
	}, nil
}

func installMac(home, exe string, o Options) (*Result, error) {
	path := filepath.Join(home, "Applications", "agentbox.command")
	if o.Remove {
		return removeAll(path)
	}
	script := "#!/bin/sh\nexec " + strconv.Quote(exe) + "\n"
	if err := writeFile(path, []byte(script), 0o755); err != nil {
		return nil, err
	}
	return &Result{
		Paths: []string{path},
		Hint:  "Drag it from ~/Applications to the Dock; it opens agentbox in Terminal.",
	}, nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
