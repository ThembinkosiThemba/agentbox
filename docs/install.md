# Installation

agentbox is a single static binary with no dependencies. The install scripts are the quickest way to get it onto your PATH.

## Install script

**Linux and macOS**

```sh
curl -fsSL https://raw.githubusercontent.com/ThembinkosiThemba/agentbox/main/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/ThembinkosiThemba/agentbox/main/install.ps1 | iex
```

On Windows, running `install.sh` from Git Bash, MSYS2 or Cygwin hands off to `install.ps1`, so the same command works there too.

The scripts:

1. detect your OS and CPU (x64 or ARM) and download the matching binary from the latest GitHub release
2. check it against the release's `checksums.txt`, and stop without installing anything if it doesn't match
3. install it:
   - **Linux and macOS:** to `~/.local/bin/agentbox`
   - **Windows:** to `%LOCALAPPDATA%\Programs\agentbox\agentbox.exe`
4. make sure new terminals can find it (see [PATH changes](#path-changes))
5. add agentbox to the Start menu (Windows) or the applications menu (Linux desktops), so you can pin it to the taskbar or dock. See [`agentbox shortcut`](cli.md#shortcut)

Run the script again to update. On Windows this works even while agentbox is running.

### Options

Set these environment variables before running the script:

| Variable | Effect |
| --- | --- |
| `AGENTBOX_VERSION` | Install a specific version, e.g. `0.2.0` (default: latest) |
| `AGENTBOX_INSTALL_DIR` | Install somewhere else |
| `AGENTBOX_NO_MODIFY_PATH=1` | Don't change PATH; just print what to add |
| `AGENTBOX_NO_SHORTCUT=1` | Don't add the Start menu / applications menu entry |

```sh
curl -fsSL https://raw.githubusercontent.com/ThembinkosiThemba/agentbox/main/install.sh | AGENTBOX_VERSION=0.2.0 sh
```

```powershell
$env:AGENTBOX_VERSION = "0.2.0"; irm https://raw.githubusercontent.com/ThembinkosiThemba/agentbox/main/install.ps1 | iex
```

### PATH changes

If the install directory is already on your PATH, nothing is changed. Otherwise:

| System | Change |
| --- | --- |
| Windows | Added to your **user** PATH, which PowerShell, cmd, Git Bash and VS Code terminals all read |
| Linux and macOS, bash | `export PATH=...` added to `~/.bashrc`, and also `~/.bash_profile` on macOS |
| Linux and macOS, zsh | `export PATH=...` added to `~/.zshrc` (or `$ZDOTDIR/.zshrc`) |
| Linux and macOS, fish | `fish_add_path` written to `~/.config/fish/conf.d/agentbox.fish` |
| Linux and macOS, other shells | `export PATH=...` added to `~/.profile` |

On Linux and macOS the shell is read from `$SHELL`. Each line is added only once, under an `# agentbox` comment, so it's easy to find and remove. Open a new terminal afterwards for the change to take effect.

## Manual download

Download the binary for your platform from [Releases](https://github.com/ThembinkosiThemba/agentbox/releases/latest) and put it in a directory on your PATH:

| Platform | x64 | ARM |
| --- | --- | --- |
| Windows | `agentbox-windows-amd64.exe` | `agentbox-windows-arm64.exe` |
| Linux | `agentbox-linux-amd64` | `agentbox-linux-arm64` |
| macOS | `agentbox-darwin-amd64` (Intel) | `agentbox-darwin-arm64` (Apple Silicon) |

On Linux and macOS, rename the file to `agentbox` and make it executable:

```sh
chmod +x agentbox
```

On macOS, a binary downloaded through a browser is quarantined by Gatekeeper. Clear the quarantine flag with:

```sh
xattr -d com.apple.quarantine agentbox
```

Each release includes `checksums.txt` with the SHA-256 of every binary:

```sh
sha256sum -c checksums.txt --ignore-missing                       # Linux
shasum -a 256 -c checksums.txt --ignore-missing                   # macOS
```

```powershell
(Get-FileHash agentbox-windows-amd64.exe -Algorithm SHA256).Hash  # Windows: compare with checksums.txt
```

## Building from source

This requires Go 1.26 or later.

```sh
git clone https://github.com/ThembinkosiThemba/agentbox
cd agentbox
go install .        # installs to $(go env GOPATH)/bin
agentbox shortcut   # optional: add it to the Start menu / app launcher
```

## Uninstall

Run `agentbox shortcut --remove` to remove the launcher, then delete the binary:
- **Linux and macOS:** `~/.local/bin/agentbox`
- **Windows:** `%LOCALAPPDATA%\Programs\agentbox`

Then remove the `# agentbox` lines from your shell config, or the folder from your user PATH on Windows.

agentbox's own data is kept separately:
- **Windows:** `%LOCALAPPDATA%\agentbox`
- **Linux and macOS:** `~/.local/state/agentbox`

Delete that folder to remove run history and logs.
