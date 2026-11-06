#requires -Version 5.1
<#
.SYNOPSIS
  Install the latest phi release on Windows.
.DESCRIPTION
  Mirrors scripts/install.sh for Windows: resolves the latest GitHub release
  (or a pinned PHI_VERSION), downloads the Windows zip, verifies its SHA-256
  checksum, extracts phi.exe into the phi bin dir, and adds that dir to the
  user PATH when missing.
.PARAMETER Version
  Release tag to install (default: latest), e.g. v0.4.0. A leading "v" is
  added if missing. May also be set via the PHI_VERSION env var.
.PARAMETER InstallDir
  Directory to install phi.exe into (default: ~\.phi\bin, where phi already
  keeps downloaded tools like fd/ripgrep). May also be set via the
  PHI_INSTALL_DIR env var.
.PARAMETER Repo
  GitHub repo in owner/name form (default: pulseaiclub/phi). May also be set
  via the PHI_REPO env var.
.EXAMPLE
  irm https://raw.githubusercontent.com/pulseaiclub/phi/main/scripts/install.ps1 | iex

  $env:PHI_VERSION = 'vX.Y.Z'
  irm https://raw.githubusercontent.com/pulseaiclub/phi/main/scripts/install.ps1 | iex
.NOTES
  Windows/arm64 builds are not published; this script only supports amd64.
  Set GITHUB_TOKEN to raise GitHub API rate limits.
#>
[CmdletBinding()]
param(
    [string]$Version,
    [string]$InstallDir,
    [string]$Repo
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # much faster Invoke-WebRequest
# Windows PowerShell 5.1 defaults to older TLS on some systems; GitHub requires TLS 1.2+.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

# ---- config (params win over env vars) ----
if (-not $Repo) {
    $Repo = $env:PHI_REPO
    if (-not $Repo) { $Repo = 'pulseaiclub/phi' }
}
if (-not $Version) { $Version = $env:PHI_VERSION }
if (-not $InstallDir) {
    $InstallDir = $env:PHI_INSTALL_DIR
    if (-not $InstallDir) { $InstallDir = Join-Path $HOME '.phi\bin' }
}
$Token = if ($env:GITHUB_TOKEN) { $env:GITHUB_TOKEN } else { '' }

# ---- platform ----
# PROCESSOR_ARCHITEW6432 is set when 32-bit PowerShell runs on 64-bit Windows.
$procArch = $env:PROCESSOR_ARCHITEW6432
if (-not $procArch) { $procArch = $env:PROCESSOR_ARCHITECTURE }
$goarch = switch ($procArch) {
    'AMD64' { 'amd64' }
    'ARM64' { throw 'windows/arm64 builds are not published; download the amd64 zip from https://github.com/pulseaiclub/phi/releases if it runs on your machine' }
    default { throw "unsupported CPU arch: $procArch" }
}

# ---- resolve tag ----
$api = "https://api.github.com/repos/$Repo"
$dl  = "https://github.com/$Repo/releases/download"

$headers = @{
    'Accept'                  = 'application/vnd.github+json'
    'X-GitHub-Api-Version'    = '2022-11-28'
}
if ($Token) { $headers['Authorization'] = "Bearer $Token" }

if ($Version) {
    $Tag = $Version
    if ($Tag -notmatch '^v') { $Tag = "v$Tag" }
}
else {
    Write-Host "phi install: querying latest release..."
    try {
        $rel = Invoke-RestMethod -Uri "$api/releases/latest" -Headers $headers
    }
    catch {
        throw "failed to query $api/releases/latest: $($_.Exception.Message) (publish a release first, or set PHI_VERSION=vX.Y.Z)"
    }
    $Tag = [string]$rel.tag_name
}

# GoReleaser .Version strips the leading v from the tag.
$ver    = $Tag -replace '^[vV]', ''
$asset  = "phi_${ver}_windows_${goarch}.zip"
$sums   = "checksums_${ver}.txt"
$assetUrl = "$dl/$Tag/$asset"
$sumsUrl  = "$dl/$Tag/$sums"

Write-Host "phi install: $Tag (windows/$goarch)"
Write-Host "phi install: $assetUrl"

# ---- temp workspace ----
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("phi-install-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
    # ---- download ----
    Write-Host "phi install: downloading checksums..."
    $sumsPath = Join-Path $tmp $sums
    Invoke-WebRequest -Uri $sumsUrl -OutFile $sumsPath -Headers $headers

    Write-Host "phi install: downloading archive..."
    $zipPath = Join-Path $tmp $asset
    Invoke-WebRequest -Uri $assetUrl -OutFile $zipPath -Headers $headers

    # ---- verify ----
    Write-Host "phi install: verifying checksum..."
    $want = $null
    foreach ($line in (Get-Content -LiteralPath $sumsPath)) {
        $line = $line.Trim()
        if ($line -eq '') { continue }
        $fields = $line -split '\s+'
        if ($fields.Count -ge 2 -and $fields[1] -eq $asset) { $want = $fields[0]; break }
    }
    if (-not $want) { throw "checksum for $asset not found in $sums" }

    $got = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash
    if ($got -ne $want.ToUpperInvariant()) {
        throw "checksum mismatch for ${asset}: got $got, want $want"
    }

    # ---- extract + install ----
    Write-Host "phi install: extracting..."
    $extractDir = Join-Path $tmp 'extracted'
    New-Item -ItemType Directory -Path $extractDir -Force | Out-Null
    Expand-Archive -LiteralPath $zipPath -DestinationPath $extractDir -Force
    $newBin = Join-Path $extractDir 'phi.exe'
    if (-not (Test-Path -LiteralPath $newBin -PathType Leaf)) {
        throw "extracted archive does not contain phi.exe at $newBin"
    }

    Write-Host "phi install: installing to $InstallDir"
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $dest = Join-Path $InstallDir 'phi.exe'
    if (Test-Path -LiteralPath $dest) {
        # Windows locks running executables; rename-then-move mirrors
        # internal/util/update's replaceBinary so a live phi fails loudly
        # instead of half-overwriting.
        $bak = "$dest.old"
        Remove-Item -LiteralPath $bak -Force -ErrorAction SilentlyContinue
        try {
            Rename-Item -LiteralPath $dest -NewName (Split-Path $bak -Leaf)
            Move-Item -LiteralPath $newBin -Destination $dest -Force
        }
        catch {
            # Roll back: restore the old binary if we already renamed it away.
            if (-not (Test-Path -LiteralPath $dest) -and (Test-Path -LiteralPath $bak)) {
                Rename-Item -LiteralPath $bak -NewName (Split-Path $dest -Leaf) -ErrorAction SilentlyContinue
            }
            throw "could not replace $dest (close any running phi and retry): $($_.Exception.Message)"
        }
        Remove-Item -LiteralPath $bak -Force -ErrorAction SilentlyContinue
    }
    else {
        Move-Item -LiteralPath $newBin -Destination $dest -Force
    }

    Write-Host "phi install: installed $Tag -> $dest"
}
finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

# ---- PATH (user scope, idempotent) ----
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$onPath = $false
if ($userPath) {
    foreach ($entry in $userPath.Split(';')) {
        if ($entry.TrimEnd('\') -ieq $InstallDir.TrimEnd('\')) { $onPath = $true; break }
    }
}
if (-not $onPath) {
    $newUserPath = if ($userPath) { "$InstallDir;$userPath" } else { $InstallDir }
    [Environment]::SetEnvironmentVariable('Path', $newUserPath, 'User')
    Write-Host "phi install: added $InstallDir to user PATH (new terminals only)"
}
# Make phi reachable in THIS session too.
$env:Path = "$InstallDir;$env:Path"

Write-Host ""
Write-Host "phi install: $Tag installed successfully!"
Write-Host ""
Write-Host "Next steps:"
Write-Host "  1. phi config          # add a model id + your provider's api key"
Write-Host "  2. phi                 # start the TUI"
Write-Host ""
Write-Host "Or set PHI_MODEL and PHI_API_KEY, then run 'phi'."
