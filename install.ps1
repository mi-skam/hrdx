<#
.SYNOPSIS
  hrdx installer for Windows.

.DESCRIPTION
  Downloads the matching release archive from GitHub, verifies its
  sha256 against the release's checksums.txt, extracts the binary, and
  puts it on a directory added to your user PATH.

.PARAMETER Version
  Release tag (e.g. v0.1.0). Defaults to "latest". Same as $env:HRDX_VERSION.

.PARAMETER Prefix
  Install directory. Defaults to $env:LOCALAPPDATA\Programs\hrdx (created
  if missing, added to the user PATH if it isn't already). Same as
  $env:HRDX_PREFIX.

.EXAMPLE
  irm https://www.hrdx.dev/install.ps1 | iex

.EXAMPLE
  $env:HRDX_VERSION = "v0.1.0"; irm https://www.hrdx.dev/install.ps1 | iex

.EXAMPLE
  .\install.ps1 -Version v0.1.0 -Prefix C:\tools\bin
#>
param(
    [string]$Version = $(if ($env:HRDX_VERSION) { $env:HRDX_VERSION } else { "latest" }),
    [string]$Prefix = $env:HRDX_PREFIX
)

$ErrorActionPreference = "Stop"

$Owner = "patriceckhart"
$Repo = "hrdx"
$Binary = "hrdx"
$OS = "windows"

function Write-Msg([string]$Text) { Write-Host "==> $Text" }
function Write-Warn([string]$Text) { Write-Host "warn: $Text" -ForegroundColor Yellow }
function Die([string]$Text) { Write-Host "error: $Text" -ForegroundColor Red; exit 1 }

# ---- detect arch ----

switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { $Arch = "amd64" }
    "ARM64" { $Arch = "arm64" }
    default { Die "unsupported arch: $env:PROCESSOR_ARCHITECTURE" }
}

# ---- resolve version ----

if ($Version -eq "latest") {
    try {
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Owner/$Repo/releases/latest"
    } catch {
        Die "could not resolve latest version: $_"
    }
    $Version = $release.tag_name
    if (-not $Version) { Die "could not resolve latest version" }
}
if ($Version -notmatch "^v") { $Version = "v$Version" }
$VerNum = $Version.TrimStart("v")

# ---- pick an install prefix ----

if (-not $Prefix) {
    $Prefix = Join-Path $env:LOCALAPPDATA "Programs\hrdx"
}
New-Item -ItemType Directory -Force -Path $Prefix | Out-Null

# ---- download + verify + extract ----

$Archive = "${Binary}_${VerNum}_${OS}_${Arch}.zip"
$BaseUrl = "https://github.com/$Owner/$Repo/releases/download/$Version"
$ArchiveUrl = "$BaseUrl/$Archive"
$ChecksumsUrl = "$BaseUrl/checksums.txt"

$Tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Force -Path $Tmp | Out-Null
try {
    Write-Msg "downloading $Archive"
    try {
        Invoke-WebRequest -Uri $ArchiveUrl -OutFile (Join-Path $Tmp $Archive)
    } catch {
        Die "download failed: $ArchiveUrl"
    }

    Write-Msg "verifying checksum"
    try {
        Invoke-WebRequest -Uri $ChecksumsUrl -OutFile (Join-Path $Tmp "checksums.txt")
    } catch {
        Die "download failed: $ChecksumsUrl"
    }

    $checksumLine = Select-String -Path (Join-Path $Tmp "checksums.txt") -Pattern ([regex]::Escape($Archive) + '$') |
        Select-Object -First 1
    if (-not $checksumLine) { Die "no checksum for $Archive in checksums.txt" }
    $expected = ($checksumLine.Line -split '\s+')[0].ToLower()

    $actual = (Get-FileHash -Path (Join-Path $Tmp $Archive) -Algorithm SHA256).Hash.ToLower()
    if ($expected -ne $actual) {
        Die "checksum mismatch: expected $expected, got $actual"
    }

    Write-Msg "extracting"
    Expand-Archive -Path (Join-Path $Tmp $Archive) -DestinationPath $Tmp -Force

    $BinaryPath = Join-Path $Tmp "$Binary.exe"
    if (-not (Test-Path $BinaryPath)) {
        Die "archive did not contain a '$Binary.exe' binary"
    }

    Write-Msg "installing to $Prefix\$Binary.exe"
    Copy-Item -Path $BinaryPath -Destination (Join-Path $Prefix "$Binary.exe") -Force
} finally {
    Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}

# ---- PATH ----

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$pathEntries = @()
if ($userPath) { $pathEntries = $userPath -split ";" }
if ($pathEntries -notcontains $Prefix) {
    [Environment]::SetEnvironmentVariable("Path", ($userPath, $Prefix -join ";").Trim(";"), "User")
    $env:Path = "$env:Path;$Prefix"
    Write-Msg "added $Prefix to your user PATH (new terminals will pick it up automatically)"
}

# ---- done ----

$installedVersion = & (Join-Path $Prefix "$Binary.exe") --version 2>$null
if (-not $installedVersion) { $installedVersion = "hrdx" }
Write-Msg "installed $installedVersion"
Write-Msg "run:  hrdx          (interactive tui)"
Write-Msg "run:  hrdx --help   (all flags)"
