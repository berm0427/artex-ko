# Register the separately installed REA/Ghidra WSL backend with ARTEX.
# SPDX-License-Identifier: AGPL-3.0-or-later
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string]$GhidraDir,
    [string]$Distro = 'Ubuntu',
    [string]$ReaEntry = '',
    [string]$PsqlPath = '',
    [switch]$SkipDiscoveryTest
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$configPath = Join-Path $repoRoot 'config.json'
$bridge = Join-Path $PSScriptRoot 'rea-wsl-mcp-bridge.mjs'
$node = (Get-Command node -ErrorAction Stop).Source
if (-not (Test-Path -LiteralPath $configPath)) {
    throw 'config.json이 없습니다. install-windows.ps1을 먼저 실행하세요.'
}
if (-not $GhidraDir.StartsWith('/') -or ($ReaEntry -and -not $ReaEntry.StartsWith('/'))) {
    throw 'GhidraDir와 ReaEntry는 WSL 절대 경로여야 합니다.'
}
& wsl.exe -d $Distro -- true
if ($LASTEXITCODE -ne 0) { throw "WSL 배포판을 실행하지 못했습니다: $Distro" }
& wsl.exe -d $Distro -- test -x /usr/bin/node
if ($LASTEXITCODE -ne 0) { throw 'WSL의 /usr/bin/node가 없습니다. WSL 안에 Node.js를 설치하세요.' }
if (-not $ReaEntry) {
    $npmRoot = (& wsl.exe -d $Distro -- npm root -g | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $npmRoot.StartsWith('/')) {
        throw 'WSL의 npm 전역 설치 경로를 찾지 못했습니다.'
    }
    $ReaEntry = "$npmRoot/rea-agents/scripts/rea.mjs"
}
& wsl.exe -d $Distro -- test -f $ReaEntry
if ($LASTEXITCODE -ne 0) { throw "REA 엔트리 파일이 없습니다: $ReaEntry" }
& wsl.exe -d $Distro -- test -d $GhidraDir
if ($LASTEXITCODE -ne 0) { throw "Ghidra 디렉터리가 없습니다: $GhidraDir" }
& $node $bridge --self-test
if ($LASTEXITCODE -ne 0) { throw 'Windows/WSL 경로 변환 테스트 실패' }

if (-not $SkipDiscoveryTest) {
    $goCommand = Get-Command go -ErrorAction SilentlyContinue
    $goExe = if ($goCommand) { $goCommand.Source } else {
        Get-ChildItem -LiteralPath (Join-Path $repoRoot '.runtime') -Directory -Filter 'go*' -ErrorAction SilentlyContinue |
            ForEach-Object { Join-Path $_.FullName 'go\bin\go.exe' } |
            Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
    }
    if (-not $goExe) { throw 'Go가 없습니다. install-windows.ps1을 먼저 실행하세요.' }
    $env:REA_MCP_NODE = $node
    $env:REA_MCP_ENTRY = $ReaEntry
    $env:REA_MCP_LAUNCHER = $bridge
    $env:REA_MCP_WSL_DISTRO = $Distro
    $env:REA_MCP_WSL_GHIDRA_DIR = $GhidraDir
    try {
        Push-Location $repoRoot
        try {
            & $goExe test ./server -run '^TestREAStdioDiscoveryIntegration$' -count=1 -v
        } finally { Pop-Location }
        if ($LASTEXITCODE -ne 0) { throw 'REA MCP 도구 검색 테스트 실패' }
    } finally {
        'REA_MCP_NODE','REA_MCP_ENTRY','REA_MCP_LAUNCHER','REA_MCP_WSL_DISTRO','REA_MCP_WSL_GHIDRA_DIR' |
            ForEach-Object { Remove-Item "Env:$_" -ErrorAction SilentlyContinue }
    }
}

$config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
$postgresRoot = Get-ChildItem -LiteralPath (Join-Path $repoRoot '.runtime') -Directory -ErrorAction SilentlyContinue |
    Where-Object Name -Like 'postgresql-*' | Sort-Object Name -Descending | Select-Object -First 1
$psql = $PsqlPath
if (-not $psql -and $postgresRoot) { $psql = Join-Path $postgresRoot.FullName 'pgsql\bin\psql.exe' }
if (-not $psql -or -not (Test-Path -LiteralPath $psql)) {
    $toolsDir = [IO.Path]::GetFullPath((Join-Path $repoRoot '..\..\tools'))
    $psql = Get-ChildItem -LiteralPath $toolsDir -Directory -Filter 'postgresql-*' -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending | ForEach-Object { Join-Path $_.FullName 'pgsql\bin\psql.exe' } |
        Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
}
if (-not $psql -or -not (Test-Path -LiteralPath $psql)) {
    $psqlCommand = Get-Command psql -ErrorAction SilentlyContinue
    if (-not $psqlCommand) { throw 'psql.exe가 없습니다. install-windows.ps1을 먼저 실행하세요.' }
    $psql = $psqlCommand.Source
}
$argsJson = @($bridge, $Distro, $ReaEntry, $GhidraDir) | ConvertTo-Json -Compress
$sql = @'
WITH server_row AS (
    INSERT INTO mcp_servers(name, transport, command, args, env, enabled)
    VALUES ('rea', 'stdio', :'node', :'args'::jsonb, '{}'::jsonb, true)
    ON CONFLICT (name) DO UPDATE SET
        transport = EXCLUDED.transport, command = EXCLUDED.command,
        args = EXCLUDED.args, env = EXCLUDED.env, enabled = true
    RETURNING id
)
INSERT INTO agent_visibility(agent_id, resource_kind, resource_id, enabled)
SELECT a.id, 'mcp', s.id, true FROM agents a CROSS JOIN server_row s WHERE a.key = 'worker'
ON CONFLICT (agent_id, resource_kind, resource_id, mcp_tool_name)
DO UPDATE SET enabled = true;
'@
$env:PGPASSWORD = [string]$config.database.password
try {
    $sql | & $psql -X -v ON_ERROR_STOP=1 -v "node=$node" -v "args=$argsJson" `
        -h ([string]$config.database.host) -p ([string]$config.database.port) `
        -U ([string]$config.database.user) -d ([string]$config.database.dbname)
    if ($LASTEXITCODE -ne 0) { throw 'ARTEX DB의 REA 등록 실패. ARTEX를 한 번 시작해 스키마를 만드세요.' }
} finally {
    Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue
}
Write-Host 'REA 연결 등록 완료. ARTEX 시스템 → MCP에서 rea의 도구 새로고침을 누르세요.'
