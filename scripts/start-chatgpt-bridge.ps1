# Start the already-authorized local ChatGPT bridge without printing its secret key.
# Interactive OAuth consent is deliberately kept in the one-time `login` command.
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$bridge = Join-Path $env:LOCALAPPDATA 'ARTEX\chatgpt-plan-bridge.exe'
$endpoint = 'http://127.0.0.1:18789/v1/responses'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))

if (-not (Test-Path -LiteralPath $bridge -PathType Leaf)) {
    Write-Error 'ChatGPT bridge executable not found. Run scripts\install-windows.ps1 from this repository first.'
    exit 1
}

# `status` prints the local profile key. Keep its entire output in memory only.
$status = @(& $bridge status 2>&1)
if ($LASTEXITCODE -ne 0) {
    Write-Error 'Could not read ChatGPT bridge status.'
    exit 1
}
$connected = [regex]::Match(($status -join "`n"), '(?m)^Connected:\s*(true|false)\s*$').Groups[1].Value
$key = [regex]::Match(($status -join "`n"), '(?m)^Local profile key:\s*(\S+)\s*$').Groups[1].Value
if ($connected -ne 'true' -or [string]::IsNullOrWhiteSpace($key)) {
    Write-Error 'ChatGPT is not connected. Run chatgpt-plan-bridge.exe login once, then retry this BAT.'
    exit 1
}

function Test-Bridge {
    param([string]$Secret)
    $client = [Net.Http.HttpClient]::new()
    $client.Timeout = [TimeSpan]::FromSeconds(3)
    $request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::Get, $endpoint)
    $request.Headers.Authorization = [Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer', $Secret)
    try {
        $response = $client.SendAsync($request).GetAwaiter().GetResult()
        try { return [int]$response.StatusCode } finally { $response.Dispose() }
    } catch {
        return 0
    } finally {
        $request.Dispose()
        $client.Dispose()
    }
}

function Test-ARTEXRunning {
    $expected = Join-Path $repoRoot 'artex.exe'
    return (@(Get-CimInstance Win32_Process -Filter "name='artex.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.ExecutablePath -and ([IO.Path]::GetFullPath($_.ExecutablePath) -ieq $expected) }).Count -gt 0)
}

$result = Test-Bridge -Secret $key
if ($result -eq 405) {
    Write-Host '[ARTEX-KO] ChatGPT bridge is already running.'
    if (Test-ARTEXRunning) { Write-Host '[ARTEX-KO] ARTEX is already running.'; exit 2 }
    exit 0
}

$listener = Get-NetTCPConnection -LocalAddress '127.0.0.1' -LocalPort 18789 -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
if ($listener) {
    $owner = Get-CimInstance Win32_Process -Filter "ProcessId=$($listener.OwningProcess)" -ErrorAction SilentlyContinue
    $sameBridge = $owner -and $owner.ExecutablePath -and ([IO.Path]::GetFullPath($owner.ExecutablePath) -ieq [IO.Path]::GetFullPath($bridge))
    if (-not $sameBridge) {
        Write-Error 'Port 18789 belongs to another process. Nothing was stopped.'
        exit 1
    }
    if (Test-ARTEXRunning) {
        Write-Error 'An ARTEX process is running, but its bridge does not accept the current local key. Stop the task and restart the bridge before retrying.'
        exit 1
    }
    # The verified bridge owns the port and ARTEX is stopped: replace its stale instance.
    Stop-Process -Id $listener.OwningProcess -ErrorAction Stop
    for ($i = 0; $i -lt 20; $i++) {
        Start-Sleep -Milliseconds 100
        if (-not (Get-NetTCPConnection -LocalAddress '127.0.0.1' -LocalPort 18789 -State Listen -ErrorAction SilentlyContinue)) { break }
    }
    if (Get-NetTCPConnection -LocalAddress '127.0.0.1' -LocalPort 18789 -State Listen -ErrorAction SilentlyContinue) {
        Write-Error 'The old ChatGPT bridge did not release port 18789.'
        exit 1
    }
}

$process = Start-Process -FilePath $bridge -ArgumentList 'serve' -WindowStyle Hidden -PassThru
for ($i = 0; $i -lt 40; $i++) {
    Start-Sleep -Milliseconds 150
    $result = Test-Bridge -Secret $key
    if ($result -eq 405) {
        Write-Host '[ARTEX-KO] ChatGPT bridge started.'
        if (Test-ARTEXRunning) { Write-Host '[ARTEX-KO] ARTEX is already running.'; exit 2 }
        exit 0
    }
    if ($process.HasExited) { break }
}

Write-Error "ChatGPT bridge did not become ready (local status $result). Check its login and port 18789."
exit 1
