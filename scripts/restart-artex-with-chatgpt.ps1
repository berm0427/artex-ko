# Full restart of this checkout's ARTEX, local bridge and dedicated PostgreSQL.
# Do not stop unrelated listeners or other PostgreSQL clusters.
[CmdletBinding()]
param([string]$ReplacementExe)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Net.Http
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$serverExe = Join-Path $repoRoot 'artex.exe'
$bridgeExe = Join-Path $env:LOCALAPPDATA 'ARTEX\chatgpt-plan-bridge.exe'
$launcher = Join-Path $repoRoot 'START-ARTEX-WITH-CHATGPT.bat'
$pgDataCandidates = @(
    (Join-Path $repoRoot '.runtime\pgdata'),
    (Join-Path (Split-Path $repoRoot -Parent) 'artex-pgdata')
)

function Same-Path([string]$A, [string]$B) {
    if (-not $A -or -not $B) { return $false }
    return [IO.Path]::GetFullPath($A).Equals([IO.Path]::GetFullPath($B), [StringComparison]::OrdinalIgnoreCase)
}

if ($ReplacementExe) {
    $ReplacementExe = [IO.Path]::GetFullPath($ReplacementExe)
    if (-not (Test-Path -LiteralPath $ReplacementExe -PathType Leaf)) {
        throw "Replacement ARTEX executable not found: $ReplacementExe"
    }
    if (Same-Path $ReplacementExe $serverExe) {
        throw 'Replacement executable must be distinct from the running ARTEX executable.'
    }
    $go = Get-Command go -ErrorAction SilentlyContinue
    if (-not $go) { throw 'Go is required to verify that the replacement binary embeds the UI.' }
    $buildInfo = @(& $go.Source version -m $ReplacementExe 2>&1)
    if ($LASTEXITCODE -ne 0 -or -not ($buildInfo | Where-Object { $_ -match '^\s*build\s+-tags=.*\bembedui\b' })) {
        throw 'Replacement binary has no embedui build tag; refusing to replace the working web UI.'
    }
}

function Wait-PortRelease([int]$Port) {
    for ($i = 0; $i -lt 40; $i++) {
        if (-not (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)) { return }
        Start-Sleep -Milliseconds 250
    }
    throw "Port $Port did not close. No unrelated process was stopped."
}

if (-not (Test-Path -LiteralPath $serverExe -PathType Leaf) -or
    -not (Test-Path -LiteralPath $bridgeExe -PathType Leaf) -or
    -not (Test-Path -LiteralPath $launcher -PathType Leaf)) {
    throw 'ARTEX executable, bridge or launcher is missing.'
}

$serverProcesses = @(Get-CimInstance Win32_Process -Filter "name='artex.exe'" |
    Where-Object { Same-Path $_.ExecutablePath $serverExe })
foreach ($server in $serverProcesses) {
    $parent = Get-CimInstance Win32_Process -Filter "ProcessId=$($server.ParentProcessId)" -ErrorAction SilentlyContinue
    if ($parent -and $parent.Name -eq 'cmd.exe' -and
        $parent.CommandLine -match 'START-ARTEX-WITH-CHATGPT\.bat|START-ARTEX-KO\.cmd|start\.bat') {
        Stop-Process -Id $parent.ProcessId -ErrorAction Stop
    }
}
foreach ($server in $serverProcesses) {
    Stop-Process -Id $server.ProcessId -ErrorAction Stop
}
Wait-PortRelease 8787

$bridgeListeners = @(Get-NetTCPConnection -LocalAddress '127.0.0.1' -LocalPort 18789 -State Listen -ErrorAction SilentlyContinue)
foreach ($listener in $bridgeListeners) {
    $owner = Get-CimInstance Win32_Process -Filter "ProcessId=$($listener.OwningProcess)" -ErrorAction SilentlyContinue
    if (-not $owner -or -not (Same-Path $owner.ExecutablePath $bridgeExe)) {
        throw 'Port 18789 belongs to another program. It was not stopped.'
    }
}
$bridgeProcesses = @(Get-CimInstance Win32_Process -Filter "name='chatgpt-plan-bridge.exe'" |
    Where-Object { Same-Path $_.ExecutablePath $bridgeExe })
foreach ($bridge in $bridgeProcesses) {
    Stop-Process -Id $bridge.ProcessId -ErrorAction Stop
}
Wait-PortRelease 18789

$pgListener = Get-NetTCPConnection -LocalAddress '127.0.0.1' -LocalPort 5433 -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
if ($pgListener) {
    $pg = Get-CimInstance Win32_Process -Filter "ProcessId=$($pgListener.OwningProcess)" -ErrorAction SilentlyContinue
    $matchedData = $null
    foreach ($candidate in $pgDataCandidates) {
        if ($pg -and $pg.Name -eq 'postgres.exe' -and
            $pg.CommandLine -and $pg.CommandLine.Contains([IO.Path]::GetFullPath($candidate).Replace('\','/'))) {
            $matchedData = $candidate
            break
        }
    }
    if (-not $matchedData) { throw 'Port 5433 is not the dedicated ARTEX PostgreSQL cluster. It was not stopped.' }
    $pgCtl = Join-Path (Split-Path $pg.ExecutablePath -Parent) 'pg_ctl.exe'
    & $pgCtl -D $matchedData -m fast -w stop | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Dedicated PostgreSQL did not stop cleanly.' }
    Wait-PortRelease 5433
}

Write-Host '[ARTEX-KO] Dedicated processes stopped. Starting full stack...'
if ($ReplacementExe) {
    $backup = Join-Path $repoRoot ("artex.pre-replacement-{0}.exe" -f (Get-Date -Format 'yyyyMMdd-HHmmss'))
    Copy-Item -LiteralPath $serverExe -Destination $backup -ErrorAction Stop
    Copy-Item -LiteralPath $ReplacementExe -Destination $serverExe -Force -ErrorAction Stop
    Write-Host "[ARTEX-KO] ARTEX binary replaced. Previous binary: $backup"
}
$launchProcess = Start-Process -FilePath $env:ComSpec -ArgumentList @('/c','START-ARTEX-WITH-CHATGPT.bat') `
    -WorkingDirectory $repoRoot -WindowStyle Hidden -PassThru

for ($i = 0; $i -lt 80; $i++) {
    Start-Sleep -Milliseconds 500
    $web = Get-NetTCPConnection -LocalPort 8787 -State Listen -ErrorAction SilentlyContinue
    $bridge = Get-NetTCPConnection -LocalAddress '127.0.0.1' -LocalPort 18789 -State Listen -ErrorAction SilentlyContinue
    if ($web -and $bridge) { break }
    if ($launchProcess.HasExited) { throw 'ARTEX launcher exited before both services became ready.' }
}
if (-not $web -or -not $bridge) { throw 'ARTEX or bridge did not start within 40 seconds.' }

# Validate the running listener against the current profile key without printing it.
$status = @(& $bridgeExe status 2>&1)
if ($LASTEXITCODE -ne 0) { throw 'Could not read local bridge status.' }
$key = [regex]::Match(($status -join "`n"), '(?m)^Local profile key:\s*(\S+)\s*$').Groups[1].Value
if ([string]::IsNullOrWhiteSpace($key)) { throw 'Local bridge key is unavailable.' }
$client = [Net.Http.HttpClient]::new()
$client.Timeout = [TimeSpan]::FromSeconds(3)
$request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::Get, 'http://127.0.0.1:18789/v1/responses')
$request.Headers.Authorization = [Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer', $key)
try {
    $response = $client.SendAsync($request).GetAwaiter().GetResult()
    try { $code = [int]$response.StatusCode } finally { $response.Dispose() }
} finally {
    $request.Dispose()
    $client.Dispose()
    $key = $null
}
if ($code -ne 405) { throw "Running bridge rejected its current profile key (HTTP $code)." }
Write-Host '[ARTEX-KO] Full restart complete. Bridge authentication passed; http://127.0.0.1:8787'
