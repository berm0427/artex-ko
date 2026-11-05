# ARTEX 한국어판

ARTEX는 LLM 에이전트가 허가된 환경에서 보안 점검 작업을 수행하는 도구입니다. [원본 ARTEX](https://github.com/Autumn-27/ARTEX)의 한국어판이며, Go 서버·내장 웹 UI·PostgreSQL로 구성됩니다. 사용자가 소유하거나 명시적으로 점검 허가를 받은 대상에만 사용하세요. 자동화된 판단과 성공 보고는 반드시 실제 증거로 확인해야 합니다.

이 문서는 **Windows 11 x64 로컬 설치**를 기준으로 합니다. [中文](README.zh.md) · [English](README.en.md) · [라이선스](LICENSE)

## 설치

Git과 Node.js/npm을 설치한 뒤 PowerShell에서 실행합니다. 설치 스크립트가 Go·PostgreSQL과 웹 UI가 내장된 ARTEX 실행 파일을 준비합니다. 첫 설치에는 인터넷 연결과 빌드 시간이 필요합니다.

```powershell
git clone https://github.com/berm0427/artex-ko.git
cd artex-ko
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\install-windows.ps1 -NoStart -SkipLMStudio
.\START-ARTEX-KO.cmd
```

브라우저에서 <http://127.0.0.1:8787/setup>을 열어 관리자 계정을 만든 다음 **시스템 → LLM**에서 모델을 설정하세요. LM Studio를 자동 연결하려면 설치 명령에서 `-SkipLMStudio`를 빼면 됩니다. 모델 파일 자체는 자동 다운로드되지 않습니다.

기본 포트는 웹 `8787`, PostgreSQL `5433`입니다. 로컬 설정·데이터베이스·API 키·작업 기록은 Git으로 복사되지 않습니다.

## 실행 동작

ARTEX는 다음 의도에 확인된 사실과 해당 실행 폴더의 위치를 인계해 중복 계측을 줄입니다. 비교 실행에서는 시작 전에 `ARTEX_EVIDENCE_HANDOFF=off`로 끌 수 있습니다. 적용 범위와 12개 자료 평가 기준은 [작업 인계 평가](docs/evidence-handoff-evaluation.md)에 정리했습니다.

목표가 비공개 결과의 실제 응답 획득이라면 실행자는 확인된 사실에 `goal_stage`(`reachability`/`fault`/`access`/`output`)를 선택적으로 기록할 수 있습니다. 계획자와 다음 실행자는 최근 확인 사실의 단계 색인을 보지만, 서로 다른 경로의 단계를 자동으로 합치거나 단계 표식만으로 성공 판정하지 않습니다. 예를 들어 프로세스 충돌은 `fault`의 증거일 수 있지만 `access`나 `output`의 증거는 아닙니다. 자료 조회 실패도 대상의 거부 응답과 구분하며, 무해한 국소 검증에 최종 출력의 사전 증명을 요구하지 않습니다.

작업의 `timeout_seconds`는 개발·평가 실행에서 고정된 비교 창이 필요할 때만 지정합니다(90분은 `5400`). 생략하거나 `0`이면 기존 엔진에서 작업 전체의 고정 종료 시각을 두지 않습니다. 운영 시 장기 풀이를 허용하더라도 무제한 재시도나 무제한 자원 사용을 뜻하지 않습니다. 반복 응답 감지, 실행별 예산, 범위·안전 중단 기준은 계속 적용하고, 작업별 비용·자원 상한은 별도로 설정·검증해야 합니다. 이전에 지정한 제한은 해당 작업에 그대로 적용되며 자동으로 해제되지 않습니다.

## 재시작

서버 소스 수정은 실행 중인 `artex.exe`에 자동 반영되지 않습니다. 설치 스크립트로 다시 빌드한 뒤 아래 BAT로 이 설치본의 ARTEX와 전용 PostgreSQL을 다시 시작하세요.

```powershell
.\START-ARTEX-WITH-LOCAL-MODEL.bat
```

재시작은 진행 중인 작업을 끊을 수 있습니다. 다른 프로그램이 포트를 사용 중이면 스크립트는 종료하지 않고 중단합니다.

## 자주 겪는 문제

| 증상 | 확인할 것 |
| --- | --- |
| “프런트엔드가 내장되어 있지 않습니다” | `embedui` 빌드가 필요합니다. `scripts/install-windows.ps1`로 다시 빌드하세요. |

REA 역공학 도구가 필요한 경우 ARTEX 설치 후 `powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\install-rea-wsl.ps1 -Distro Ubuntu`를 실행하고 **시스템 → MCP**에서 확인하세요. WSL 설치에는 관리자 권한이나 재부팅이 필요할 수 있습니다.

방어·탐지 관점의 자료는 [가이드](docs/defense-ko.md)를 참고하세요. 이 도구와 자동화된 조언은 검증을 대체하지 않으며, 허가 범위와 현지 법률을 지킬 책임은 사용자에게 있습니다. 라이선스는 [AGPL-3.0](LICENSE)입니다.
