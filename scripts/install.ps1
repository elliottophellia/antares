<#
.SYNOPSIS
  Antares installer for Windows (PowerShell).

.DESCRIPTION
  Downloads the prebuilt antares.exe for your platform from the project's GitHub
  Releases and installs it. No build tools required.

.EXAMPLE
  irm https://raw.githubusercontent.com/enowdev/antares/main/scripts/install.ps1 | iex

.NOTES
  Env knobs (set before running):
    $env:ANTARES_PREFIX    install dir; default $env:LOCALAPPDATA\Antares
    $env:ANTARES_VERSION   specific release tag; default latest
    $env:ANTARES_REPO      owner/name; default enowdev/antares

  While the repository is private, releases are not publicly downloadable.
  Install the GitHub CLI (https://cli.github.com), run 'gh auth login', and this
  script uses it automatically. Once the repo is public, no auth is needed.
#>

$ErrorActionPreference = 'Stop'

$Repo    = if ($env:ANTARES_REPO)    { $env:ANTARES_REPO }    else { 'enowdev/antares' }
$Version = if ($env:ANTARES_VERSION) { $env:ANTARES_VERSION } else { 'latest' }
$Prefix  = if ($env:ANTARES_PREFIX)  { $env:ANTARES_PREFIX }  else { Join-Path $env:LOCALAPPDATA 'Antares' }
$BinDir  = Join-Path $Prefix 'bin'

function Info($m) { Write-Host "==> $m" -ForegroundColor Cyan }
function Warn($m) { Write-Host "warning: $m" -ForegroundColor Yellow }
function Die($m)  { Write-Host "error: $m" -ForegroundColor Red; exit 1 }
function Have($c) { [bool](Get-Command $c -ErrorAction SilentlyContinue) }

# ---- detect arch ------------------------------------------------------------
$arch = $env:PROCESSOR_ARCHITECTURE
switch ($arch) {
  'AMD64' { $goarch = 'amd64' }
  'ARM64' { $goarch = 'arm64' }
  default { Die "unsupported architecture '$arch'" }
}
Info "platform: windows/$goarch"

function AssetFor($ver) { "antares_${ver}_windows_${goarch}.exe" }

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
$dest = Join-Path $BinDir 'antares.exe'
$tmp  = Join-Path ([System.IO.Path]::GetTempPath()) ("antares-" + [System.Guid]::NewGuid().ToString('N') + '.exe')

# ---- fetch ------------------------------------------------------------------
if (Have 'gh') {
  Info "downloading via GitHub CLI ($Repo, $Version)"
  $ver = $Version
  if ($ver -eq 'latest') {
    $ver = (gh release view --repo $Repo --json tagName -q .tagName)
    if (-not $ver) { Die "could not read the latest release. Is 'gh' authenticated? Try: gh auth login" }
  }
  $asset = AssetFor $ver
  gh release download $ver --repo $Repo --pattern $asset --output $tmp --clobber
  if (-not (Test-Path $tmp)) { Die "no asset '$asset' in release $ver." }
} else {
  if ($Version -eq 'latest') {
    Die "without the GitHub CLI you must pass a version, e.g. `$env:ANTARES_VERSION='v0.1.0'. (If the repo is still private, install gh from https://cli.github.com and run 'gh auth login'.)"
  }
  $asset = AssetFor $Version
  $url = "https://github.com/$Repo/releases/download/$Version/$asset"
  Info "downloading $url"
  try { Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing }
  catch { Die "download failed. If the repo is still private, install the GitHub CLI (gh) and run 'gh auth login', then re-run." }
}

if (-not (Test-Path $tmp) -or (Get-Item $tmp).Length -eq 0) { Die "downloaded file is empty." }

# ---- install ----------------------------------------------------------------
Move-Item -Force $tmp $dest
Info "installed $dest"
try { & $dest --version } catch {}

# ---- PATH -------------------------------------------------------------------
$userPath = [Environment]::GetEnvironmentVariable('Path','User')
if ($userPath -notlike "*$BinDir*") {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$BinDir", 'User')
  Warn "added $BinDir to your user PATH — open a NEW terminal for it to take effect."
} else {
  Info "run 'antares' from anywhere (open a new terminal if not found yet)"
}

Write-Host ""
Info "next: run 'antares setup' to configure a provider, then 'antares' or 'antares serve'"
