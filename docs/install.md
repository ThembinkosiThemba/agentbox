# Installation

agentbox is a single static binary with no dependencies. Download the one for your platform from [Releases](https://github.com/ThembinkosiThemba/agentbox/releases/latest):

| Platform | x64 | ARM |
| --- | --- | --- |
| Windows | `agentbox-windows-amd64.exe` | `agentbox-windows-arm64.exe` |
| Linux | `agentbox-linux-amd64` | `agentbox-linux-arm64` |
| macOS | `agentbox-darwin-amd64` (Intel) | `agentbox-darwin-arm64` (Apple Silicon) |

## Linux and macOS

```sh
os=$(uname -s | tr A-Z a-z); arch=$(uname -m)
[ "$arch" = x86_64 ] && arch=amd64; [ "$arch" = aarch64 ] && arch=arm64
mkdir -p ~/.local/bin
curl -fsSL -o ~/.local/bin/agentbox \
  "https://github.com/ThembinkosiThemba/agentbox/releases/latest/download/agentbox-$os-$arch"
chmod +x ~/.local/bin/agentbox
```

Make sure `~/.local/bin` is on your `PATH`.

On macOS, a binary downloaded through a browser is quarantined by Gatekeeper. Clear the quarantine flag with:

```sh
xattr -d com.apple.quarantine ./agentbox-darwin-*
```

## Windows

In PowerShell:

```powershell
$dir = "$env:LOCALAPPDATA\Programs\agentbox"
New-Item -ItemType Directory -Force $dir | Out-Null
Invoke-WebRequest -OutFile "$dir\agentbox.exe" `
  https://github.com/ThembinkosiThemba/agentbox/releases/latest/download/agentbox-windows-amd64.exe
[Environment]::SetEnvironmentVariable("Path", "$([Environment]::GetEnvironmentVariable('Path','User'));$dir", "User")
```

On ARM devices, use `agentbox-windows-arm64.exe` instead. Open a new terminal afterwards so the `PATH` change takes effect.

## Verifying downloads

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
go build -o agentbox .
```

## Checking the install

```sh
agentbox version
```
