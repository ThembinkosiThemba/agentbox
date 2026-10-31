package launcher

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// The shortcut is written by the Windows Script Host's COM object, driven
// from PowerShell, which every supported Windows has. Paths travel in
// environment variables so no quoting can go wrong.
const lnkScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$paths = @($env:AGENTBOX_LNK_START)
if ($env:AGENTBOX_LNK_DESKTOP -eq '1') {
    $paths += Join-Path ([Environment]::GetFolderPath('Desktop')) 'agentbox.lnk'
}
$shell = New-Object -ComObject WScript.Shell
foreach ($p in $paths) {
    if ($env:AGENTBOX_LNK_REMOVE -eq '1') {
        if (Test-Path -LiteralPath $p) { Remove-Item -LiteralPath $p -Force; Write-Output $p }
        continue
    }
    New-Item -ItemType Directory -Force (Split-Path $p) | Out-Null
    $s = $shell.CreateShortcut($p)
    $s.TargetPath = $env:AGENTBOX_LNK_EXE
    $s.WorkingDirectory = $env:USERPROFILE
    $s.IconLocation = "$($env:AGENTBOX_LNK_EXE),0"
    $s.Description = 'See and manage what your coding agents run'
    $s.Save()
    Write-Output $p
}
`

// Install adds an "agentbox" Start Menu shortcut for exe (and optionally one
// on the desktop), or removes them.
func Install(exe string, o Options) (*Result, error) {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		return nil, fmt.Errorf("APPDATA is not set")
	}
	start := filepath.Join(appdata, `Microsoft\Windows\Start Menu\Programs\agentbox.lnk`)

	// -EncodedCommand takes the script whole; -Command - reads stdin line by
	// line and mangles multi-line blocks.
	u := utf16.Encode([]rune(lnkScript))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	cmd.Env = append(os.Environ(),
		"AGENTBOX_LNK_START="+start,
		"AGENTBOX_LNK_EXE="+exe,
		"AGENTBOX_LNK_DESKTOP="+flag(o.Desktop),
		"AGENTBOX_LNK_REMOVE="+flag(o.Remove),
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("create shortcut: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	res := &Result{}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			res.Paths = append(res.Paths, l)
		}
	}
	if !o.Remove {
		res.Hint = "Find agentbox in the Start menu, then right-click it and choose Pin to taskbar."
	}
	return res, nil
}

func flag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
