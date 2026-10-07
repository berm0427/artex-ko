# ARTEX 한국어판 Windows 사용자 범위 설치 스크립트
# Copyright (C) 2026 ARTEX contributors
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# SPDX-License-Identifier: AGPL-3.0-or-later

[CmdletBinding()]
param(
    [int]$PostgresPort = 5433,
    [int]$WebPort = 8787,
    [switch]$NoStart,
    [switch]$SkipLMStudio,
    [string]$LMStudioModel = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$runtimeRoot = Join-Path $repoRoot '.runtime'
$postgresRoot = Join-Path $runtimeRoot 'postgresql-17.11'
$postgresBin = Join-Path $postgresRoot 'pgsql\bin'
$postgresData = Join-Path $runtimeRoot 'pgdata'
$postgresLog = Join-Path $runtimeRoot 'postgres.log'

function Write-Step([string]$Message) {
    Write-Host "[ARTEX-KO] $Message" -ForegroundColor Cyan
}

function Assert-Command([string]$Name, [string]$Guide) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "$Name 명령을 찾을 수 없습니다. $Guide"
    }
}

if (-not [Environment]::Is64BitOperatingSystem) {
    throw '현재 설치 스크립트는 64비트 Windows만 지원합니다.'
}

New-Item -ItemType Directory -Path $runtimeRoot -Force | Out-Null
Assert-Command npm 'Node.js LTS를 먼저 설치하세요: https://nodejs.org/'

# 관리자 권한 없이 사용할 수 있도록 공식 Go ZIP을 저장소 내부에 설치합니다.
$goCommand = Get-Command go -ErrorAction SilentlyContinue
if ($goCommand) {
    $goExe = $goCommand.Source
    Write-Step "시스템 Go 사용: $(& $goExe version)"
} else {
    Write-Step '공식 Go Windows ZIP 조회 및 설치'
    $releases = Invoke-RestMethod -Uri 'https://go.dev/dl/?mode=json'
    $stable = $releases | Where-Object stable | Select-Object -First 1
    $archive = $stable.files | Where-Object {
        $_.os -eq 'windows' -and $_.arch -eq 'amd64' -and $_.kind -eq 'archive'
    } | Select-Object -First 1
    if (-not $archive) { throw '공식 Go Windows amd64 아카이브를 찾지 못했습니다.' }

    $goRoot = Join-Path $runtimeRoot $stable.version
    $goZip = Join-Path $runtimeRoot $archive.filename
    if (-not (Test-Path -LiteralPath $goZip)) {
        Invoke-WebRequest -Uri "https://go.dev/dl/$($archive.filename)" -OutFile $goZip
    }
    $actualHash = (Get-FileHash -LiteralPath $goZip -Algorithm SHA256).Hash
    if ($actualHash -ne $archive.sha256) {
        throw "Go 아카이브 SHA-256 불일치: $actualHash"
    }
    if (-not (Test-Path -LiteralPath (Join-Path $goRoot 'go\bin\go.exe'))) {
        New-Item -ItemType Directory -Path $goRoot -Force | Out-Null
        Expand-Archive -LiteralPath $goZip -DestinationPath $goRoot
    }
    $goExe = Join-Path $goRoot 'go\bin\go.exe'
    Write-Step "Go 설치 완료: $(& $goExe version)"
}

Write-Step '한국어 프런트엔드 의존성 설치 및 정적 빌드'
Push-Location (Join-Path $repoRoot 'web')
try {
    & npm ci
    if ($LASTEXITCODE -ne 0) { throw 'npm ci 실패' }
    $env:NEXT_EXPORT = '1'
    & (Join-Path (Get-Location) 'node_modules\.bin\next.cmd') build
    if ($LASTEXITCODE -ne 0) {
        Write-Warning '최신 SWC 네이티브 캐시 검사가 Windows ACL과 충돌했습니다. 호환 SWC로 한 번 더 시도합니다.'
        & npm install --no-save --package-lock=false `
            '@swc/core@1.15.18' '@swc/core-win32-x64-msvc@1.15.18'
        if ($LASTEXITCODE -ne 0) { throw 'SWC 호환 패키지 설치 실패' }
        $nestedSwc = Join-Path (Get-Location) 'node_modules\next-intl\node_modules\@swc'
        if (Test-Path -LiteralPath $nestedSwc) {
            [IO.Directory]::Delete((Resolve-Path -LiteralPath $nestedSwc).Path, $true)
        }
        & (Join-Path (Get-Location) 'node_modules\.bin\next.cmd') build
        if ($LASTEXITCODE -ne 0) {
            throw 'Next.js 정적 빌드 실패. 출력된 SWC 또는 Node 오류를 확인하세요.'
        }
    }
} finally {
    Remove-Item Env:NEXT_EXPORT -ErrorAction SilentlyContinue
    Pop-Location
}

$dist = Join-Path $repoRoot 'server\webui\dist'
if (Test-Path -LiteralPath $dist) {
    $resolvedDist = (Resolve-Path -LiteralPath $dist).Path
    if (-not $resolvedDist.StartsWith($repoRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "예상하지 못한 UI 경로: $resolvedDist"
    }
    [System.IO.Directory]::Delete($resolvedDist, $true)
}
New-Item -ItemType Directory -Path $dist -Force | Out-Null
Copy-Item -Path (Join-Path $repoRoot 'web\out\*') -Destination $dist -Recurse -Force

Write-Step '한국어 UI를 내장한 artex.exe 컴파일'
$env:CGO_ENABLED = '0'
Push-Location $repoRoot
try {
    & $goExe build -tags embedui -trimpath -o artex.exe .\cmd\artex
    if ($LASTEXITCODE -ne 0) { throw 'Go 빌드 실패' }
} finally {
    Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}

# PostgreSQL 공식 Windows 바이너리를 서비스 등록 없이 사용자 범위에 설치합니다.
$postgresZip = Join-Path $runtimeRoot 'postgresql-17.11-5-windows-x64-binaries.zip'
$postgresUrl = 'https://get.enterprisedb.com/postgresql/postgresql-17.11-5-windows-x64-binaries.zip'
$postgresSha256 = '80379B2C04D51C30225532E0AE04509899141E9957ED096FE749D7FD9DF8F82F'
if (-not (Test-Path -LiteralPath (Join-Path $postgresBin 'postgres.exe'))) {
    Write-Step 'PostgreSQL 17.11 공식 Windows 바이너리 설치'
    if (-not (Test-Path -LiteralPath $postgresZip)) {
        Invoke-WebRequest -Uri $postgresUrl -OutFile $postgresZip
    }
    $actualHash = (Get-FileHash -LiteralPath $postgresZip -Algorithm SHA256).Hash
    if ($actualHash -ne $postgresSha256) {
        throw "PostgreSQL 아카이브 SHA-256 불일치: $actualHash"
    }
    New-Item -ItemType Directory -Path $postgresRoot -Force | Out-Null
    Expand-Archive -LiteralPath $postgresZip -DestinationPath $postgresRoot
}

$passwordFile = Join-Path $runtimeRoot 'pg-password.tmp'
$configPath = Join-Path $repoRoot 'config.json'
if (-not (Test-Path -LiteralPath (Join-Path $postgresData 'PG_VERSION'))) {
    Write-Step '로컬 PostgreSQL 데이터베이스 초기화'
    $randomBytes = [byte[]]::new(32)
    [Security.Cryptography.RandomNumberGenerator]::Fill($randomBytes)
    $dbPassword = [Convert]::ToBase64String($randomBytes).TrimEnd('=').Replace('+','-').Replace('/','_')
    [IO.File]::WriteAllText($passwordFile, $dbPassword, [Text.UTF8Encoding]::new($false))
    try {
        & (Join-Path $postgresBin 'initdb.exe') -D $postgresData -U artex `
            --encoding=UTF8 --auth-local=trust --auth-host=scram-sha-256 `
            --pwfile=$passwordFile --no-locale
        if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL initdb 실패' }
    } finally {
        [IO.File]::Delete($passwordFile)
    }

    $config = [ordered]@{
        database = [ordered]@{
            host = '127.0.0.1'; port = $PostgresPort; user = 'artex'
            password = $dbPassword; dbname = 'artex'; sslmode = 'disable'
        }
        skill_dir = 'skills'
    }
    [IO.File]::WriteAllText(
        $configPath,
        ($config | ConvertTo-Json -Depth 4),
        [Text.UTF8Encoding]::new($false)
    )
    & icacls.exe $configPath /inheritance:r /grant:r `
        "$env:USERNAME`:F" 'SYSTEM:F' 'Administrators:F' | Out-Null
} elseif (-not (Test-Path -LiteralPath $configPath)) {
    throw '기존 PostgreSQL 데이터는 있지만 config.json이 없습니다. 자동으로 비밀번호를 복구할 수 없습니다.'
}

$pgIsReady = Join-Path $postgresBin 'pg_isready.exe'
& $pgIsReady -h 127.0.0.1 -p $PostgresPort -U artex *> $null
if ($LASTEXITCODE -ne 0) {
    Write-Step "PostgreSQL 시작: 127.0.0.1:$PostgresPort"
    $pgArgs = @(
        '-D', $postgresData, '-l', $postgresLog,
        '-o', "-p $PostgresPort -h 127.0.0.1", 'start'
    )
    & (Join-Path $postgresBin 'pg_ctl.exe') @pgArgs
    if ($LASTEXITCODE -ne 0) { throw "PostgreSQL 시작 실패: $postgresLog" }
}

$launcher = @"
@echo off
chcp 65001 >nul 2>&1
setlocal
set "PGBIN=$postgresBin"
set "PGDATA=$postgresData"
set "PGLOG=$postgresLog"
"%PGBIN%\pg_isready.exe" -h 127.0.0.1 -p $PostgresPort -U artex >nul 2>&1
if not errorlevel 1 goto postgres_ready
echo [ARTEX-KO] PostgreSQL 시작 중...
"%PGBIN%\pg_ctl.exe" -D "%PGDATA%" -l "%PGLOG%" -o "-p $PostgresPort -h 127.0.0.1" start
if errorlevel 1 goto postgres_failed
:postgres_ready
echo [ARTEX-KO] http://127.0.0.1:$WebPort
if defined ARTEX_KO_LAUNCHER_CHECK exit /b 0
call "%~dp0start.bat" -addr :$WebPort
exit /b %ERRORLEVEL%
:postgres_failed
echo [ARTEX-KO] PostgreSQL 시작 실패. 로그: %PGLOG% 1>&2
exit /b 1
"@
# cmd.exe can misparse redirections such as 2>&1 when a generated batch file
# uses LF-only newlines. Always emit a native CRLF launcher, even under pwsh 7.
$launcher = ($launcher -replace "`r?`n", "`r`n")
[IO.File]::WriteAllText(
    (Join-Path $repoRoot 'START-ARTEX-KO.cmd'),
    $launcher,
    [Text.UTF8Encoding]::new($false)
)

# ARTEX가 테이블 마이그레이션을 끝낸 뒤 LM Studio 프로필을 멱등 등록합니다.
# LM Studio가 없는 환경에서는 설치 전체를 실패시키지 않고 UI 설정 경로를 유지합니다.
if (-not $SkipLMStudio) {
    $lmsCandidate = Join-Path $env:USERPROFILE '.lmstudio\bin\lms.exe'
    $lmsCommand = Get-Command lms -ErrorAction SilentlyContinue
    if ($lmsCommand -or (Test-Path -LiteralPath $lmsCandidate)) {
        $temporaryArtex = $null
        try {
            try {
                Invoke-WebRequest -Uri "http://127.0.0.1:$WebPort/setup" -UseBasicParsing -TimeoutSec 2 | Out-Null
            } catch {
                Write-Step '초기 데이터베이스 스키마 생성'
                $temporaryArtex = Start-Process -FilePath (Join-Path $repoRoot 'artex.exe') `
                    -ArgumentList @('-addr', ":$WebPort") -WorkingDirectory $repoRoot `
                    -WindowStyle Hidden -PassThru
                $ready = $false
                for ($attempt = 0; $attempt -lt 60; $attempt++) {
                    Start-Sleep -Milliseconds 500
                    try {
                        Invoke-WebRequest -Uri "http://127.0.0.1:$WebPort/setup" `
                            -UseBasicParsing -TimeoutSec 2 | Out-Null
                        $ready = $true
                        break
                    } catch { }
                }
                if (-not $ready) { throw 'ARTEX 초기 스키마 생성 대기 시간이 초과되었습니다.' }
            }

            $lmArgs = @()
            if ($LMStudioModel) { $lmArgs += @('-Model', $LMStudioModel) }
            & (Join-Path $PSScriptRoot 'configure-lmstudio.ps1') @lmArgs
            if ($LASTEXITCODE -ne 0) { throw 'LM Studio 자동 연결 스크립트 실패' }
        } catch {
            Write-Warning "LM Studio 자동 연결을 건너뜁니다: $($_.Exception.Message)"
            Write-Warning '.\scripts\configure-lmstudio.ps1 로 나중에 다시 설정할 수 있습니다.'
        } finally {
            if ($temporaryArtex -and -not $temporaryArtex.HasExited) {
                Stop-Process -Id $temporaryArtex.Id -Force -ErrorAction SilentlyContinue
                $temporaryArtex.WaitForExit()
            }
        }
    } else {
        Write-Warning 'LM Studio가 없어 자동 연결을 건너뜁니다. 설치 후 .\scripts\configure-lmstudio.ps1 을 실행하세요.'
    }
}

Write-Host ''
Write-Host '설치 완료' -ForegroundColor Green
Write-Host "실행: $(Join-Path $repoRoot 'START-ARTEX-KO.cmd')"
Write-Host "접속: http://127.0.0.1:$WebPort/setup"
Write-Warning '본인 소유 또는 명시적으로 허가받은 격리 환경에서만 사용하세요.'

if (-not $NoStart) {
    & (Join-Path $repoRoot 'start.bat') -addr ":$WebPort"
}
