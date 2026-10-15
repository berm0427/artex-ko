# Install WSL Ubuntu (if needed), REA and Ghidra, then register with ARTEX.
# SPDX-License-Identifier: AGPL-3.0-or-later
[CmdletBinding()]
param(
    [string]$Distro = 'Ubuntu',
    [switch]$SkipDiscoveryTest
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ($Distro -notmatch '^[A-Za-z0-9._-]+$') { throw 'Invalid WSL distribution name.' }
if (-not (Get-Command wsl.exe -ErrorAction SilentlyContinue)) {
    throw 'wsl.exe is unavailable. Enable Windows Subsystem for Linux and restart Windows.'
}

& wsl.exe -d $Distro -- true 2>$null
if ($LASTEXITCODE -ne 0) {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $admin = ([Security.Principal.WindowsPrincipal]$identity).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if (-not $admin) {
        throw "WSL $Distro is not ready. Open PowerShell as Administrator and rerun this script."
    }
    & wsl.exe --install -d $Distro
    if ($LASTEXITCODE -ne 0) { throw 'WSL installation failed. See the Microsoft WSL installation guide.' }
    Write-Host 'WSL installation requested. Restart Windows if asked, launch Ubuntu once to create its Linux account, then rerun this script.'
    return
}

$linuxScript = (Join-Path $PSScriptRoot 'install-rea-wsl.sh')
$absoluteScript = [IO.Path]::GetFullPath($linuxScript)
if ($absoluteScript -notmatch '^([A-Za-z]):\\(.*)$') { throw 'Installation script must be on a Windows drive mounted in WSL.' }
$linuxPath = '/mnt/' + $Matches[1].ToLowerInvariant() + '/' + $Matches[2].Replace('\', '/')
& wsl.exe -d $Distro -- test -f $linuxPath
if ($LASTEXITCODE -ne 0) { throw "Installation script is not available in WSL: $linuxPath" }
& wsl.exe -d $Distro -- bash $linuxPath
if ($LASTEXITCODE -ne 0) { throw 'WSL dependency installation failed; fix the preceding error and rerun.' }

$linuxHome = (& wsl.exe -d $Distro -- printenv HOME | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or -not $linuxHome.StartsWith('/')) { throw 'Cannot determine the WSL user home directory.' }
$nodeVersion = (& wsl.exe -d $Distro -- bash -c 'ls -d "$HOME"/.local/opt/node-v22.* 2>/dev/null | sort -V | tail -1' | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or -not $nodeVersion.StartsWith('/')) { throw 'Installed Node.js directory not found.' }

$params = @{
    Distro = $Distro
    LinuxNode = "$nodeVersion/bin/node"
    ReaEntry = "$linuxHome/.local/rea/node_modules/rea-agents/scripts/rea.mjs"
    GhidraDir = "$linuxHome/.local/opt/ghidra_12.1.4_PUBLIC"
    SkipDiscoveryTest = $SkipDiscoveryTest
}
& (Join-Path $PSScriptRoot 'configure-rea-wsl.ps1') @params
