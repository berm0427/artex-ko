# Bind ARTEX's event-driven help requests to an existing Codex conversation.
# Run once with -ThreadId from that conversation; normal launcher runs refresh
# the Codex executable path after desktop app updates.
[CmdletBinding()]
param([string]$ThreadId)

$ErrorActionPreference = 'Stop'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$configPath = Join-Path $repoRoot 'data\codex-help.json'
if (-not $ThreadId -and -not (Test-Path -LiteralPath $configPath -PathType Leaf)) { return }

if (-not $ThreadId) {
    $existing = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $ThreadId = [string]$existing.thread_id
}
if ($ThreadId -notmatch '^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$') {
    throw 'Codex thread ID must be a UUID.'
}

$candidates = @()
if ($env:CODEX_CLI_PATH) { $candidates += $env:CODEX_CLI_PATH }
$command = Get-Command codex -ErrorAction SilentlyContinue
if ($command -and $command.Source) { $candidates += $command.Source }
$binRoot = Join-Path $env:LOCALAPPDATA 'OpenAI\Codex\bin'
if (Test-Path -LiteralPath $binRoot -PathType Container) {
    $candidates += @(Get-ChildItem -LiteralPath $binRoot -Filter codex.exe -File -Recurse |
        Sort-Object LastWriteTimeUtc -Descending | Select-Object -ExpandProperty FullName)
}
$cliPath = $candidates | Where-Object { $_ -and (Test-Path -LiteralPath $_ -PathType Leaf) } | Select-Object -First 1
if (-not $cliPath) { throw 'Codex CLI was not found; help connection cannot be enabled.' }

$config = @{ thread_id = $ThreadId; cli_path = [IO.Path]::GetFullPath($cliPath) } | ConvertTo-Json -Compress
$dir = Split-Path $configPath -Parent
if (-not (Test-Path -LiteralPath $dir -PathType Container)) { New-Item -ItemType Directory -Path $dir | Out-Null }
[IO.File]::WriteAllText($configPath, $config + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
Write-Host '[ARTEX-KO] Codex help connection ready.'
