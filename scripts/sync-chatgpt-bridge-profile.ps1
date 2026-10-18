# Synchronize the local bridge credential with ARTEX's PostgreSQL profile
# before starting ARTEX. Never print the credential or place it on a command line.
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$configPath = Join-Path $repoRoot 'config.json'
$bridge = Join-Path $env:LOCALAPPDATA 'ARTEX\chatgpt-plan-bridge.exe'
$psqlCandidates = @(
    (Join-Path $repoRoot '.runtime\postgresql-17.11\pgsql\bin\psql.exe'),
    (Join-Path (Split-Path (Split-Path $repoRoot -Parent) -Parent) 'tools\postgresql-17.11\pgsql\bin\psql.exe')
)
$psql = $psqlCandidates | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
if (-not $psql) {
    $command = Get-Command psql.exe -ErrorAction SilentlyContinue
    if ($command) { $psql = $command.Source }
}
if (-not $psql -or -not (Test-Path -LiteralPath $configPath -PathType Leaf)) {
    Write-Error 'PostgreSQL client or config.json is missing. Run scripts\install-windows.ps1 first.'
    exit 1
}

$status = @(& $bridge status 2>&1)
if ($LASTEXITCODE -ne 0) { throw 'Could not read ChatGPT bridge status.' }
$statusText = $status -join "`n"
$connected = [regex]::Match($statusText, '(?m)^Connected:\s*(true|false)\s*$').Groups[1].Value
$key = [regex]::Match($statusText, '(?m)^Local profile key:\s*(\S+)\s*$').Groups[1].Value
if ($connected -ne 'true' -or $key -notmatch '^[A-Za-z0-9_-]{40,}$') {
    throw 'ChatGPT bridge is disconnected or its local key is invalid.'
}

$config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
$db = $config.database
$env:PGPASSWORD = [string]$db.password
try {
    $query = "SELECT CASE WHEN to_regclass('public.llm_profiles') IS NULL THEN 'missing' ELSE 'ready' END;"
    $schema = $query | & $psql -X -A -t -v ON_ERROR_STOP=1 -h ([string]$db.host) `
        -p ([string]$db.port) -U ([string]$db.user) -d ([string]$db.dbname)
    if ($LASTEXITCODE -ne 0) { throw 'Could not inspect ARTEX database.' }
    if (($schema | Select-Object -Last 1) -eq 'missing') {
        Write-Host '[ARTEX-KO] LLM profiles are not initialized yet; bridge sync skipped.'
        exit 0
    }

    # The key contains URL-safe base64 characters only. Pass SQL over stdin,
    # not via -v or -c, so it never appears in process arguments or logs.
    $query = "UPDATE llm_profiles SET api_key='$key', api_key_hint=chr(8230)||right('$key',4), updated_at=now() WHERE format='openai-responses' AND base_url='http://127.0.0.1:18789/v1' AND api_key IS DISTINCT FROM '$key';"
    $result = $query | & $psql -X -A -t -v ON_ERROR_STOP=1 -h ([string]$db.host) `
        -p ([string]$db.port) -U ([string]$db.user) -d ([string]$db.dbname)
    if ($LASTEXITCODE -ne 0) { throw 'Could not synchronize ARTEX bridge profile.' }
    if ($result -match '^UPDATE ([1-9][0-9]*)$') {
        Write-Host '[ARTEX-KO] ChatGPT bridge profile synchronized.'
    } else {
        Write-Host '[ARTEX-KO] ChatGPT bridge profile is current.'
    }
} finally {
    Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue
    $key = $null
    $query = $null
}
