# sabr one-line installer for Windows (PowerShell 5.1+ or 7):
#   irm https://mrzroot.github.io/sabr/install.ps1 | iex
# Downloads sabr.exe from the latest GitHub release, verifies its SHA-256, installs it to
# %LOCALAPPDATA%\sabr\bin and adds that folder to your user PATH. No admin rights needed.
# Options (environment): SABR_VERSION=v0.1.0, SABR_BIN_DIR, SABR_BASE_URL (mirror of the release assets)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { 'arm64' } else { 'amd64' }
$asset = "sabr_windows_$arch.exe"
if ($env:SABR_BASE_URL) { $base = $env:SABR_BASE_URL }
elseif ($env:SABR_VERSION) { $base = "https://github.com/mrzroot/sabr/releases/download/$($env:SABR_VERSION)" }
else { $base = 'https://github.com/mrzroot/sabr/releases/latest/download' }
$binDir = if ($env:SABR_BIN_DIR) { $env:SABR_BIN_DIR } else { Join-Path $env:LOCALAPPDATA 'sabr\bin' }
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("sabr-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

function Fetch($url, $out) {
  for ($i = 1; $i -le 15; $i++) {
    try {
      if ($url -match '^[A-Za-z]:\\|^\\\\|^file:') { Copy-Item ($url -replace '^file:///', '') $out -Force; return }
      Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $out -TimeoutSec 120
      return
    } catch {
      if ($i -eq 15) { throw }
      Write-Host "  connection dropped; retrying ($i)..." -ForegroundColor Yellow
      Start-Sleep -Seconds 2
    }
  }
}
function Join-Url($b, $n) { if ($b -match '^[A-Za-z]:\\|^\\\\') { return (Join-Path $b $n) } else { return "$b/$n" } }

try {
  Write-Host "==> Downloading $asset" -ForegroundColor Green
  $exe = Join-Path $tmp 'sabr.exe'
  Fetch (Join-Url $base $asset) $exe
  $sums = Join-Path $tmp 'SHA256SUMS'
  Fetch (Join-Url $base 'SHA256SUMS') $sums
  $line = Get-Content $sums | Where-Object { $_ -match (" " + [regex]::Escape($asset) + '$') } | Select-Object -First 1
  if (-not $line) { throw "$asset is not listed in SHA256SUMS" }
  $want = ($line -split '\s+')[0].ToLower()
  $got = (Get-FileHash -Algorithm SHA256 $exe).Hash.ToLower()
  if ($want -ne $got) { throw "checksum mismatch (expected $want, got $got)" }
  Write-Host '  checksum ok'
  $dest = Join-Path $binDir 'sabr.exe'
  Copy-Item $exe $dest -Force
  $ver = & $dest version
  Write-Host "==> Installed sabr $ver to $dest" -ForegroundColor Green
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (-not (($userPath -split ';') -contains $binDir)) {
    $newPath = if ($userPath) { "$userPath;$binDir" } else { $binDir }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    Write-Host "  added $binDir to your user PATH; open a new terminal to use 'sabr' everywhere" -ForegroundColor Yellow
  }
  $env:Path = "$binDir;$env:Path"
  Write-Host ''
  Write-Host 'Try:  sabr clone https://github.com/torvalds/linux'
  Write-Host '      sabr docker pull python:3.12-slim --load'
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
