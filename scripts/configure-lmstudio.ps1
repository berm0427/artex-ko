# ARTEX 한국어판 - LM Studio 자동 연결
# SPDX-License-Identifier: AGPL-3.0-or-later

[CmdletBinding()]
param(
    [string]$Model = '',
    [string]$ProfileName = 'LM Studio Local',
    [int]$Port = 1234,
    [ValidateRange(1, 1000)] [int]$ContextWindowK = 16,
    [ValidateRange(1, 1048576)] [int]$MaxTokens = 4096,
    [ValidateRange(1, 32)] [int]$Parallel = 2,
    [ValidateRange(1, 32)] [int]$Workers = 2,
    [string]$PsqlPath = '',
    [switch]$NoLoad
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$configPath = Join-Path $repoRoot 'config.json'
$baseUrl = "http://127.0.0.1:$Port/v1"

function Write-Step([string]$Message) {
    Write-Host "[ARTEX-KO / LM Studio] $Message" -ForegroundColor Cyan
}

function Find-Lms {
    $command = Get-Command lms -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    $candidate = Join-Path $env:USERPROFILE '.lmstudio\bin\lms.exe'
    if (Test-Path -LiteralPath $candidate) { return $candidate }
    throw 'LM Studio CLI(lms.exe)를 찾지 못했습니다. LM Studio를 설치하고 한 번 실행한 뒤 다시 시도하세요: https://lmstudio.ai/'
}

function Invoke-LmsJson([string[]]$Arguments) {
    $text = & $script:lmsExe @Arguments 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) { throw "lms $($Arguments -join ' ') 실패:`n$text" }
    return ($text | ConvertFrom-Json)
}

if (-not (Test-Path -LiteralPath $configPath)) {
    throw "ARTEX 데이터베이스 설정을 찾지 못했습니다: $configPath"
}

$lmsExe = Find-Lms
Write-Step "LM Studio CLI: $lmsExe"

$installed = @(Invoke-LmsJson @('ls', '--llm', '--json'))
if ($installed.Count -eq 0) {
    throw '설치된 LM Studio LLM이 없습니다. LM Studio에서 모델을 하나 내려받은 뒤 다시 실행하세요.'
}

if ([string]::IsNullOrWhiteSpace($Model)) {
    $loaded = @(Invoke-LmsJson @('ps', '--json') | Where-Object type -eq 'llm')
    if ($loaded.Count -gt 0) {
        $Model = [string]$loaded[0].identifier
    } else {
        $Model = [string]$installed[0].modelKey
    }
}

$match = @($installed | Where-Object {
    $identifier = if ($_.PSObject.Properties['identifier']) { [string]$_.identifier } else { '' }
    $_.modelKey -eq $Model -or $identifier -eq $Model -or $_.path -eq $Model
})
if ($match.Count -eq 0) {
    $available = ($installed | ForEach-Object modelKey) -join ', '
    throw "LM Studio에서 모델 '$Model'을 찾지 못했습니다. 설치된 모델: $available"
}

& $lmsExe server status *> $null
if ($LASTEXITCODE -ne 0) {
    Write-Step "로컬 API 서버 시작: 127.0.0.1:$Port"
    & $lmsExe server start --port $Port --bind 127.0.0.1
    if ($LASTEXITCODE -ne 0) { throw 'LM Studio 로컬 API 서버 시작 실패' }
}

if (-not $NoLoad) {
    $loaded = @(Invoke-LmsJson @('ps', '--json') | Where-Object {
        $_.type -eq 'llm' -and ($_.identifier -eq $Model -or $_.modelKey -eq $Model)
    })
    $needsReload = $loaded.Count -eq 0 -or
        [int]$loaded[0].contextLength -ne ($ContextWindowK * 1024) -or
        [int]$loaded[0].parallel -ne $Parallel
    if ($needsReload) {
        Write-Step "모델 로드: $Model (context ${ContextWindowK}K, parallel $Parallel)"
        & $lmsExe load $Model --gpu max --context-length ($ContextWindowK * 1024) `
            --parallel $Parallel --identifier $Model --yes
        if ($LASTEXITCODE -ne 0) { throw "LM Studio 모델 로드 실패: $Model" }
    }
}

Write-Step 'OpenAI 호환 API 응답 확인'
$headers = @{ Authorization = 'Bearer lm-studio' }
$body = @{
    model = $Model
    messages = @(@{ role = 'user'; content = 'Reply with OK.' })
    max_tokens = 8
    stream = $false
} | ConvertTo-Json -Depth 5
$response = Invoke-RestMethod -Uri "$baseUrl/chat/completions" -Method Post `
    -Headers $headers -ContentType 'application/json' -Body $body -TimeoutSec 60
if (-not $response.choices) { throw 'LM Studio가 정상적인 Chat Completions 응답을 반환하지 않았습니다.' }

$config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
$postgresRoot = Get-ChildItem -LiteralPath (Join-Path $repoRoot '.runtime') -Directory -ErrorAction SilentlyContinue |
    Where-Object Name -Like 'postgresql-*' | Sort-Object Name -Descending | Select-Object -First 1
$psql = $PsqlPath
if (-not $psql -and $postgresRoot) { $psql = Join-Path $postgresRoot.FullName 'pgsql\bin\psql.exe' }
if (-not $psql -or -not (Test-Path -LiteralPath $psql)) {
    $psqlCommand = Get-Command psql -ErrorAction SilentlyContinue
    if (-not $psqlCommand) { throw 'psql.exe를 찾지 못했습니다. 먼저 install-windows.ps1을 실행하세요.' }
    $psql = $psqlCommand.Source
}

$db = $config.database
$sql = @'
BEGIN;
UPDATE llm_profiles SET is_default=false WHERE is_default AND name <> :'profile_name';
INSERT INTO llm_profiles(
    name, format, base_url, model, api_key, api_key_hint,
    context_window_k, is_default, streaming, max_tokens, max_tokens_field
) VALUES (
    :'profile_name', 'openai', :'base_url', :'model', 'lm-studio', '…udio',
    :'context_k', true, true, :'max_tokens', ''
)
ON CONFLICT (name) DO UPDATE SET
    format=EXCLUDED.format, base_url=EXCLUDED.base_url, model=EXCLUDED.model,
    api_key=EXCLUDED.api_key, api_key_hint=EXCLUDED.api_key_hint,
    context_window_k=EXCLUDED.context_window_k, is_default=true,
    streaming=true, max_tokens=EXCLUDED.max_tokens, max_tokens_field='',
    updated_at=now();
INSERT INTO settings(key,value) VALUES ('workers', :'workers')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();
COMMIT;
'@

Write-Step 'ARTEX LLM 프로필 등록 및 활성화'
$env:PGPASSWORD = [string]$db.password
try {
    $sql | & $psql -X -v ON_ERROR_STOP=1 `
        -v "profile_name=$ProfileName" -v "base_url=$baseUrl" -v "model=$Model" `
        -v "context_k=$ContextWindowK" -v "max_tokens=$MaxTokens" -v "workers=$Workers" `
        -h ([string]$db.host) -p ([string]$db.port) -U ([string]$db.user) -d ([string]$db.dbname)
    if ($LASTEXITCODE -ne 0) {
        throw 'ARTEX 데이터베이스 설정 실패. ARTEX를 한 번 시작해 스키마를 만든 뒤 다시 실행하세요.'
    }
} finally {
    Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue
}

Write-Host ''
Write-Host 'LM Studio 자동 연결 완료' -ForegroundColor Green
Write-Host "모델: $Model"
Write-Host "API: $baseUrl"
Write-Host "컨텍스트: ${ContextWindowK}K / 병렬: $Parallel / ARTEX Worker: $Workers"
