# Bind ARTEX's event-driven help requests to an existing Codex conversation.
# Run once with -ThreadId from that conversation; normal launcher runs refresh
# the Codex executable path after desktop app updates.
[CmdletBinding()]
param([string]$ThreadId)

$ErrorActionPreference = 'Stop'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$configPath = Join-Path $repoRoot 'data\codex-help.json'
if (-not $ThreadId -and -not (Test-Path -LiteralPath $configPath -PathType Leaf)) { return }

$existing = $null
if (Test-Path -LiteralPath $configPath -PathType Leaf) {
    $existing = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
}
if (-not $ThreadId) {
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

$replyKey = if ($existing -and $existing.reply_key) { [string]$existing.reply_key } else { '' }
if ($replyKey -notmatch '^[A-Za-z0-9_-]{43}$') {
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    $replyKey = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+','-').Replace('/','_')
}
$config = @{ thread_id = $ThreadId; cli_path = [IO.Path]::GetFullPath($cliPath); reply_key = $replyKey } | ConvertTo-Json -Compress
$dir = Split-Path $configPath -Parent
if (-not (Test-Path -LiteralPath $dir -PathType Container)) { New-Item -ItemType Directory -Path $dir | Out-Null }
[IO.File]::WriteAllText($configPath, $config + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
Write-Host '[ARTEX-KO] Codex help connection ready.'
