# Install or update atto from its GitHub releases.
#
#   irm https://raw.githubusercontent.com/sebastianrcnt/atto/main/install.ps1 | iex
#
# $env:ATTO_VERSION = "v0.1.0" picks a release (default: the latest);
# $env:ATTO_INSTALL_DIR picks the directory (default: %LOCALAPPDATA%\Programs\atto).
# Running it again installs the latest release over the old one.
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$repo = "sebastianrcnt/atto"
$dir = if ($env:ATTO_INSTALL_DIR) { $env:ATTO_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\atto" }
$version = if ($env:ATTO_VERSION) { $env:ATTO_VERSION } else { "latest" }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "atto install: unsupported CPU $env:PROCESSOR_ARCHITECTURE" }
}
$asset = "atto_windows_$arch.exe"
$base = if ($env:ATTO_DOWNLOAD_BASE) { $env:ATTO_DOWNLOAD_BASE } elseif ($version -eq "latest") { "https://github.com/$repo/releases/latest/download" } else { "https://github.com/$repo/releases/download/$version" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("atto-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    Write-Host "Downloading $asset ($version)..."
    Invoke-WebRequest -UseBasicParsing "$base/$asset" -OutFile "$tmp\atto.exe"
    Invoke-WebRequest -UseBasicParsing "$base/checksums.txt" -OutFile "$tmp\checksums.txt"

    $want = Get-Content "$tmp\checksums.txt" | ForEach-Object {
        $f = $_ -split '\s+'
        if ($f.Count -ge 2 -and $f[1].TrimStart('*') -eq $asset) { $f[0].ToLower() }
    } | Select-Object -First 1
    if (-not $want) { throw "atto install: checksums.txt has no $asset" }
    $got = (Get-FileHash "$tmp\atto.exe" -Algorithm SHA256).Hash.ToLower()
    if ($got -ne $want) { throw "atto install: checksum mismatch for $asset; not installing" }

    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $exe = Join-Path $dir "atto.exe"
    # A running atto.exe can't be overwritten but can be renamed.
    if (Test-Path $exe) { Move-Item -Force $exe "$exe.old" }
    Move-Item -Force "$tmp\atto.exe" $exe
    Remove-Item -Force "$exe.old" -ErrorAction SilentlyContinue
    Write-Host "Installed $(& $exe -version) to $exe"

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (($userPath -split ';') -notcontains $dir) {
        [Environment]::SetEnvironmentVariable("Path", "$userPath;$dir", "User")
        Write-Host "Added $dir to your user PATH; open a new terminal to use atto."
    }
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
