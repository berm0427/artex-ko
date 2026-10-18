# Send a reviewed Codex response to one ARTEX task. Pipe the message on stdin
# so task details and findings do not appear in the process command line.
[CmdletBinding()]
param([Parameter(Mandatory=$true)][ValidateRange(1, [long]::MaxValue)][long]$TaskId)

$ErrorActionPreference = 'Stop'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$configPath = Join-Path $repoRoot 'data\codex-help.json'
$config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
if ([string]$config.reply_key -notmatch '^[A-Za-z0-9_-]{43}$') {
    throw 'Codex help reply key is not configured.'
}
$inputBytes = New-Object IO.MemoryStream
try {
    [Console]::OpenStandardInput().CopyTo($inputBytes)
    $raw = $inputBytes.ToArray()
} finally { $inputBytes.Dispose() }
try {
    $message = ([Text.UTF8Encoding]::new($false, $true)).GetString($raw).Trim()
} catch [Text.DecoderFallbackException] {
    $message = [Text.Encoding]::GetEncoding(949).GetString($raw).Trim()
}
if (-not $message) { throw 'A reviewed help message is required on stdin.' }
$payload = @{ message = "CODEX_HELP_${TaskId}: $message" } | ConvertTo-Json -Compress -Depth 4
$body = [Text.Encoding]::UTF8.GetBytes($payload)
$headers = @{ 'X-ARTEX-Codex-Help-Key' = [string]$config.reply_key }
try {
    $response = Invoke-WebRequest -Uri "http://127.0.0.1:8787/api/chat?task=$TaskId" `
        -Method Post -Headers $headers -Body $body -ContentType 'application/json; charset=utf-8' `
        -UseBasicParsing -TimeoutSec 15
    if ($response.StatusCode -notin @(200, 202)) { throw "ARTEX returned HTTP $($response.StatusCode)." }
    Write-Host "[ARTEX-KO] Reviewed help delivered to task #$TaskId."
} finally {
    $config = $null
    $headers = $null
}
