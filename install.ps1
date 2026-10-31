# agentbox installer for Windows.
#
#   irm https://raw.githubusercontent.com/ThembinkosiThemba/agentbox/main/install.ps1 | iex
#
# Environment:
#   AGENTBOX_VERSION          version to install, e.g. 0.2.0 (default: latest)
#   AGENTBOX_INSTALL_DIR      where to put agentbox.exe (default: %LOCALAPPDATA%\Programs\agentbox)
#   AGENTBOX_NO_MODIFY_PATH   set to 1 to leave PATH alone
#   AGENTBOX_BASE_URL         download from this URL instead of GitHub releases

function Install-Agentbox {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'  # Invoke-WebRequest is very slow with the progress bar
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $repo = 'ThembinkosiThemba/agentbox'

    # A 32-bit PowerShell on 64-bit Windows reports the real CPU here.
    $cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    switch ($cpu) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default { throw "agentbox install: unsupported CPU architecture: $cpu" }
    }

    $version = if ($env:AGENTBOX_VERSION) { $env:AGENTBOX_VERSION.TrimStart('v') } else { 'latest' }
    if ($env:AGENTBOX_BASE_URL) {
        $base = $env:AGENTBOX_BASE_URL.TrimEnd('/')
    } elseif ($version -eq 'latest') {
        $base = "https://github.com/$repo/releases/latest/download"
    } else {
        $base = "https://github.com/$repo/releases/download/v$version"
    }

    $asset = "agentbox-windows-$arch.exe"
    $dir = if ($env:AGENTBOX_INSTALL_DIR) { $env:AGENTBOX_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\agentbox' }
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("agentbox-" + [guid]::NewGuid())
    New-Item -ItemType Directory -Force $tmp | Out-Null

    try {
        Write-Host "Downloading $asset ($version)"
        $file = Join-Path $tmp $asset
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $file

        $sums = Join-Path $tmp 'checksums.txt'
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile $sums
        } catch {
            $sums = $null
            Write-Host "warning: checksums.txt not found; skipping verification"
        }
        if ($sums) {
            $want = Get-Content $sums | ForEach-Object {
                $parts = $_ -split '\s+'
                if ($parts.Count -ge 2 -and $parts[1].TrimStart('*') -eq $asset) { $parts[0] }
            } | Select-Object -First 1
            if (-not $want) { throw "agentbox install: $asset is not listed in checksums.txt" }
            $got = (Get-FileHash $file -Algorithm SHA256).Hash
            if ($got -ne $want.ToUpper()) { throw "agentbox install: checksum mismatch for $asset (expected $want, got $got)" }
            Write-Host "Checksum verified"
        }

        New-Item -ItemType Directory -Force $dir | Out-Null
        $target = Join-Path $dir 'agentbox.exe'
        # A running agentbox.exe can't be overwritten, but it can be renamed.
        if (Test-Path $target) {
            $old = "$target.old"
            Remove-Item $old -Force -ErrorAction SilentlyContinue
            Rename-Item $target 'agentbox.exe.old'
        }
        Move-Item $file $target -Force
        Remove-Item "$target.old" -Force -ErrorAction SilentlyContinue
        Write-Host "Installed $target"
    } finally {
        Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    # The user PATH is shared by every terminal: PowerShell, cmd, Git Bash, VS Code.
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @($userPath -split ';' | Where-Object { $_ })
    if ($entries -notcontains $dir) {
        if ($env:AGENTBOX_NO_MODIFY_PATH -eq '1') {
            Write-Host "Add $dir to your PATH to run agentbox from anywhere."
        } else {
            [Environment]::SetEnvironmentVariable('Path', (($entries + $dir) -join ';'), 'User')
            $env:Path = "$env:Path;$dir"
            Write-Host "Added $dir to your user PATH. Open a new terminal to use it everywhere."
        }
    }

    Write-Host ""
    & $target version
    Write-Host "Run 'agentbox' to open the dashboard, or 'agentbox guide' to set up your agents."
}

Install-Agentbox
