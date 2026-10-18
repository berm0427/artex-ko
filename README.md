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

## ChatGPT 구독으로 실행하기

ChatGPT Plus/Pro 연결은 일반 OpenAI API 키와 별개인 로컬 브리지를 사용합니다. 계정 동의와 첫 LLM 프로필 설정은 [ChatGPT 구독 연동 안내](docs/ChatGPT-구독-연동.md)를 따라 **한 번** 완료하세요. 이후에는 저장소 루트에서 아래 파일 하나로 브리지와 ARTEX를 실행합니다.

```powershell
.\START-ARTEX-WITH-CHATGPT.bat
```

시작 BAT는 브리지 상태를 확인하고 ARTEX의 로컬 프로필 키를 동기화합니다. 인증 키는 출력하거나 Git에 저장하지 않습니다.

## ARTEX ↔ Codex 도움 연결 (선택)

기존 **Codex 대화 하나**를 연결할 수 있습니다. 이 PC에서 해당 대화의 UUID를 확인한 뒤 한 번 실행하세요. Codex CLI가 필요합니다.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\connect-codex-help.ps1 -ThreadId "대화-UUID"
```

이후 `START-ARTEX-WITH-CHATGPT.bat`은 Codex CLI 경로를 갱신합니다. ARTEX 작업에 열린 목표는 있지만 새 실험 없이 멈추면, ARTEX 내부 스케줄러가 기존 Codex 대화에 작업 번호와 검토 요청을 보냅니다. Codex가 기록을 검토해 근거 있는 조언을 만들면 기존 ARTEX 채팅 경로(`/api/chat?task=작업번호`)로 전달할 수 있습니다. **도움 요청을 보낸 것 자체가 자동 답변이나 문제 해결을 뜻하지는 않습니다.** 목표 성공은 실제 대상 응답으로만 판정합니다.

연결 정보는 Git에서 제외되는 `data/codex-help.json`에 저장됩니다. Codex 예약 작업이나 별도의 복제 모델 세션을 만들지 않습니다. 자동 후속 작업 생성은 기본 시작 BAT에서 켜지지 않습니다. 연결을 끄려면 ARTEX를 종료한 뒤 이 로컬 연결 파일만 제거하세요.

## 재시작

서버 소스 수정은 실행 중인 `artex.exe`에 자동 반영되지 않습니다. 설치 스크립트로 다시 빌드한 뒤 아래 BAT로 이 설치본의 ARTEX·구독 브리지·전용 PostgreSQL을 재시작할 수 있습니다.

```powershell
.\RESTART-ARTEX-WITH-CHATGPT.bat
```

재시작은 진행 중인 작업을 끊을 수 있습니다. 스크립트는 다른 프로그램이 포트를 사용 중이면 종료하지 않고 중단합니다. 교체용 실행 파일을 직접 지정하는 방법은 `scripts/restart-artex-with-chatgpt.ps1 -ReplacementExe "C:\path\artex-new.exe"`이며, 웹 UI가 내장된 `embedui` 빌드만 받습니다.

## 자주 겪는 문제

| 증상 | 확인할 것 |
| --- | --- |
| “프런트엔드가 내장되어 있지 않습니다” | `embedui` 빌드가 필요합니다. `scripts/install-windows.ps1`로 다시 빌드하세요. |
| `401 invalid local bridge key` | 브리지 로그인 상태와 저장된 LLM 프로필 키가 다른 경우입니다. 구독 시작 BAT로 재시작하고 [구독 연동 안내](docs/ChatGPT-구독-연동.md)의 키 점검 절차를 확인하세요. 키 원문을 공유하지 마세요. |
| `Connected: false` | 브리지 로그인 또는 네트워크 상태를 확인하세요. `chatgpt-plan-bridge.exe login`은 사용자의 브라우저 동의가 필요합니다. |
| `auth.openai.com` DNS 오류 | 풀이 실패가 아니라 인증 서버 이름 조회 실패입니다. 네트워크·DNS 복구 후 원본 작업을 재개하세요. |
| Codex 도움 요청이 오지 않음 | 연결 파일, Codex CLI, 대상 대화 UUID를 확인하세요. 연결은 실행 중인 모든 작업을 소급해서 깨우지 않습니다. |

REA 역공학 도구가 필요한 경우 ARTEX 설치 후 `powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\install-rea-wsl.ps1 -Distro Ubuntu`를 실행하고 **시스템 → MCP**에서 확인하세요. WSL 설치에는 관리자 권한이나 재부팅이 필요할 수 있습니다.

방어·탐지 관점의 자료는 [가이드](docs/defense-ko.md)를 참고하세요. 이 도구와 자동화된 조언은 검증을 대체하지 않으며, 허가 범위와 현지 법률을 지킬 책임은 사용자에게 있습니다. 라이선스는 [AGPL-3.0](LICENSE)입니다.
