<#
One-click runner for the v34 long-loop redis experiment (protocol condition C).

Does, in order:
  1. Wait for the Docker daemon (the ARTEX worker builds/runs the redis lab).
  2. Stop any model server on 12362, then (re)start v34 under serve_openai_debug.py
     with per-POST body dumps (stock 2MB body cap kept on purpose).
  3. Wait for /health on 127.0.0.1:12362 (through the admin netsh portproxy) to
     report model artex-agentic-v34.
  4. Verify ARTEX is up on 8787.
  5. Create the redis task via POST /api/tasks bound to the v34 profile; ARTEX
     launches it automatically. Print the task id and where to watch.

Read-only on the DB/profiles. Needs: portproxy 127.0.0.1:12362 already added (admin),
ARTEX already running on 8787, the v34 profile ("ARTEX v34 C") registered.
#>
[CmdletBinding()]
param(
    [int]$Port = 12362,
    [string]$ModelName = 'artex-agentic-v34',
    [int]$ProfileId = 118,
    [string]$ArtexBase = 'http://127.0.0.1:8787',
    [string]$Priv = 'C:\Users\berm0\Documents\Codex\private\artex-learning\v34-long-loop',
    [int]$DockerWaitSec = 300,
    [int]$HealthWaitSec = 180,
    # ARTEX API JWT. Paste the artex_token value (browser cookie / devtools), or set
    # $env:ARTEX_TOKEN. Sent as Authorization: Bearer on the /api/tasks calls.
    [string]$Token = $env:ARTEX_TOKEN
)
$ErrorActionPreference = 'Stop'
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$wslRepo = '/mnt/c/Users/berm0/Documents/Codex/2026-09-15/new-chat/work/artex-ko'
$descFile = Join-Path $Priv 'redis_task_desc.txt'
$goalFile = Join-Path $Priv 'redis_task_goal.txt'

function Say($m) { Write-Host "[run-v34-redis] $m" }

# 1) Docker daemon -------------------------------------------------------------
# cmd /c keeps docker's stderr inside cmd so PowerShell's Stop policy does not turn
# a "daemon starting" stderr line into a terminating error.
function Docker-ServerUp { ((cmd /c "docker info 2>&1" | Out-String) -match 'Server Version') }
if (-not (Docker-ServerUp)) {
    $exe = @("$env:ProgramFiles\Docker\Docker\Docker Desktop.exe",
             "$env:ProgramW6432\Docker\Docker\Docker Desktop.exe",
             "$env:LocalAppData\Docker\Docker\Docker Desktop.exe") |
           Where-Object { Test-Path $_ } | Select-Object -First 1
    if ($exe) { Say "starting Docker Desktop..."; Start-Process -FilePath $exe }
}
Say "waiting for Docker daemon (up to $DockerWaitSec s)..."
$deadline = (Get-Date).AddSeconds($DockerWaitSec)
$dockerUp = $false
while ((Get-Date) -lt $deadline) {
    if (Docker-ServerUp) { $dockerUp = $true; break }
    Start-Sleep -Seconds 5
}
if (-not $dockerUp) { throw "Docker daemon not ready after $DockerWaitSec s. Open Docker Desktop and retry." }
Say 'Docker daemon up.'

# 2) (Re)start v34 debug serve, sole GPU occupant ------------------------------
# Idempotent: if a healthy v34 debug serve is already up, keep it. Otherwise stop
# any model server and launch the debug serve detached via setsid (survives the
# wsl.exe call; the running serve itself keeps the WSL distro alive).
$already = $false
try { $h0 = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/health" -TimeoutSec 3; if ($h0.ok -and [string]$h0.model -eq $ModelName) { $already = $true } } catch { }
$wslPriv = '/mnt/c/Users/berm0/Documents/Codex/private/artex-learning/v34-long-loop'
if ($already) {
    Say "v34 debug serve already healthy on $Port; keeping it."
} else {
    Say 'starting v34 debug serve (it frees port 12362 itself if stale)...'
    $cmd = "setsid bash $wslRepo/learning/winrun/serve-v34-debug.sh > $wslPriv/serve_v34_stdout.log 2>&1 < /dev/null & disown; echo launched"
    & wsl.exe -e bash -lc $cmd | Out-Null
}

# 3) health -------------------------------------------------------------------
Say "waiting for http://127.0.0.1:$Port/health ($ModelName)..."
$deadline = (Get-Date).AddSeconds($HealthWaitSec)
$served = ''
while ((Get-Date) -lt $deadline) {
    try {
        $h = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/health" -TimeoutSec 3
        if ($h.ok) { $served = [string]$h.model; if ($served -eq $ModelName) { break } }
    } catch { }
    Start-Sleep -Seconds 3
}
if ($served -ne $ModelName) { throw "v34 serve not healthy on $Port (got '$served'). Check portproxy (admin netsh) and the serve log." }
Say "serve healthy: model=$served"

# 4) ARTEX up + auth ----------------------------------------------------------
if (-not $Token) { throw "ARTEX API token required. Pass -Token <artex_token> or set `$env:ARTEX_TOKEN. (browser cookie 'artex_token')" }
$authHdr = @{ Authorization = "Bearer $Token" }
try { Invoke-RestMethod -Uri "$ArtexBase/api/tasks" -Headers $authHdr -TimeoutSec 5 | Out-Null }
catch {
    if ($_.Exception.Response.StatusCode.value__ -eq 401) { throw "ARTEX token rejected (401). Paste a fresh artex_token." }
    throw "ARTEX not reachable at $ArtexBase. Start it (START-ARTEX-WITH-LOCAL-MODEL.bat) first."
}
Say 'ARTEX reachable + authenticated on 8787.'

# 5) create + auto-launch the redis task --------------------------------------
if (-not (Test-Path $descFile) -or -not (Test-Path $goalFile)) {
    throw "task spec missing: $descFile / $goalFile"
}
$desc = [string](Get-Content -LiteralPath $descFile -Raw -Encoding UTF8)
$goal = [string](Get-Content -LiteralPath $goalFile -Raw -Encoding UTF8)
# PS 5.1 ConvertTo-Json wraps strings as {"value":..,"Count":..}; use the .NET
# JSON serializer so description/goal are plain JSON strings.
Add-Type -AssemblyName System.Web.Extensions
$ser = New-Object System.Web.Script.Serialization.JavaScriptSerializer
$body = $ser.Serialize(@{ description = $desc; goal = $goal; llm_profile_id = $ProfileId })
$resp = Invoke-RestMethod -Uri "$ArtexBase/api/tasks" -Method Post -Headers $authHdr -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($body))
$taskId = $resp.id
Say "task created & launched: #$taskId (profile $ProfileId / $ModelName)"

Write-Host ''
Write-Host "==== v34 redis run started ===="
Write-Host "task        : #$taskId  ($ArtexBase)"
Write-Host "body dumps  : $Priv\bodies-v34\"
Write-Host "serve log   : $Priv\serve_debug_v34.jsonl"
Write-Host "watch GPU   : wsl -e nvidia-smi"
Write-Host "stop serve  : wsl -e pkill -f serve_openai_debug"
