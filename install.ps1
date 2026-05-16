#Requires -Version 5.1
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$Repo      = 'kiiimatz/tailway'
$InstallDir = "$env:LOCALAPPDATA\tailway"
$BinName   = 'tailway.exe'
$Asset     = 'tailway-windows-amd64.exe'

# ── Fetch latest release tag ─────────────────────────────────────────────────
Write-Host 'Fetching latest release...'

$apiUrl  = "https://api.github.com/repos/$Repo/releases/latest"
$headers = @{ Accept = 'application/vnd.github+json' }

try {
    $release = Invoke-RestMethod -Uri $apiUrl -Headers $headers -UseBasicParsing
} catch {
    Write-Error "Could not reach GitHub API: $_"
    exit 1
}

$version = $release.tag_name
if (-not $version) {
    Write-Error 'Could not determine latest version.'
    exit 1
}

Write-Host "Latest version: $version"

# ── Download ──────────────────────────────────────────────────────────────────
$downloadUrl = "https://github.com/$Repo/releases/download/$version/$Asset"
$tmpFile     = Join-Path $env:TEMP "tailway-install-$([System.IO.Path]::GetRandomFileName()).exe"

Write-Host "Downloading $Asset..."
try {
    Invoke-WebRequest -Uri $downloadUrl -OutFile $tmpFile -UseBasicParsing
} catch {
    Write-Error "Download failed: $_"
    exit 1
}

# ── Install ───────────────────────────────────────────────────────────────────
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir | Out-Null
}

$dest = Join-Path $InstallDir $BinName

# Replace existing binary if present
if (Test-Path $dest) {
    Remove-Item $dest -Force
}

Move-Item $tmpFile $dest

Write-Host ""
Write-Host "Installed tailway $version to $dest"

# ── Add to PATH (User scope — no admin required) ──────────────────────────────
$userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')

if ($userPath -split ';' -notcontains $InstallDir) {
    $newPath = ($userPath.TrimEnd(';') + ";$InstallDir").TrimStart(';')
    [Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')

    # Also update the current session so the command works immediately
    $env:PATH = $env:PATH.TrimEnd(';') + ";$InstallDir"

    Write-Host "Added $InstallDir to your PATH."
} else {
    Write-Host "$InstallDir is already in PATH."
}

Write-Host ""
Write-Host "Run: tailway"
Write-Host "(If 'tailway' is not found, open a new terminal window.)"
