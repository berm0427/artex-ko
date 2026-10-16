# ChatGPT Plus/Pro 구독으로 ARTEX 실행하기

이 연결은 OpenAI의 공식 **Sign in with ChatGPT / ChatGPT plan usage** 흐름을 사용합니다. ARTEX의 에이전트와 도구는 그대로 두고, 로컬 브리지가 사용자 동의를 받은 OAuth 자격 증명으로 Responses API를 호출합니다. ChatGPT 대화 기록이나 Codex 앱의 내부 인증 파일을 읽지 않습니다.

> 현재 이 기능은 Windows 로컬 설치용 시험 통합입니다. 공식 구독 사용량은 무제한이 아니며 계정·모델별 제한이 적용됩니다. 유료 API로 자동 전환되지 않도록 ARTEX의 LLM 풀/작업별 대체 프로필도 확인하세요.

## 설치와 첫 연결

저장소 루트에서 PowerShell을 열고 다음 명령을 실행합니다. 빌드된 실행 파일은 Git에 포함하지 않아도 됩니다.
`scripts/install-windows.ps1`로 새 설치를 했다면 브리지도 함께 빌드되므로 아래 `go build` 단계는 건너뛸 수 있습니다.

```powershell
New-Item -ItemType Directory -Force "$env:LOCALAPPDATA\ARTEX" | Out-Null
go build -o "$env:LOCALAPPDATA\ARTEX\chatgpt-plan-bridge.exe" ./cmd/chatgpt-plan-bridge
& "$env:LOCALAPPDATA\ARTEX\chatgpt-plan-bridge.exe" login
```

`login`은 시스템 브라우저를 열고 사용자가 ChatGPT 계정·작업 공간을 선택하여 ARTEX의 구독 사용을 허용하도록 합니다. 동의하지 않으면 사용할 수 없습니다. 브라우저에 뜨는 ARTEX 이름과 권한을 확인하세요.

로그인 직후 실제 구독 모델과 ARTEX의 함수 도구 호출을 확인하려면 다음 명령을 실행합니다. 짧은 시험 추론 3회가 구독 사용량에 반영됩니다.

```powershell
& "$env:LOCALAPPDATA\ARTEX\chatgpt-plan-bridge.exe" verify
```

다음 명령으로 별도 터미널에서 로컬 브리지를 켭니다. ARTEX가 실행 중이어도 설정 전까지 기존 작업에는 영향이 없습니다. 이미 브리지가 실행 중이면 다시 실행할 필요가 없습니다.

```powershell
& "$env:LOCALAPPDATA\ARTEX\chatgpt-plan-bridge.exe" serve
```

### 실행 파일을 찾을 수 없다는 오류가 날 때

PowerShell의 `CommandNotFoundException`과 함께 `chatgpt-plan-bridge.exe`를 찾을 수 없다고 나오면, 우선 **오류가 난 바로 그 PowerShell 창**에서 설치 경로를 확인하세요.

```powershell
$bridge = Join-Path $env:LOCALAPPDATA 'ARTEX\chatgpt-plan-bridge.exe'
$bridge
Test-Path -LiteralPath $bridge
```

`False`라면 해당 Windows 환경에 브리지가 아직 빌드되지 않은 것입니다. 저장소를 받은 뒤 설치 스크립트를 실행했더라도, 브리지가 추가되기 **전**에 설치했다면 다시 빌드해야 합니다. 저장소 루트에서 다음을 실행하세요.

```powershell
New-Item -ItemType Directory -Force "$env:LOCALAPPDATA\ARTEX" | Out-Null
go build -o "$env:LOCALAPPDATA\ARTEX\chatgpt-plan-bridge.exe" ./cmd/chatgpt-plan-bridge
Test-Path -LiteralPath "$env:LOCALAPPDATA\ARTEX\chatgpt-plan-bridge.exe"
```

`True`인데도 오류가 반복되면 `$env:LOCALAPPDATA`와 `Get-Item -LiteralPath $bridge`로 실제 경로를 비교하고, 같은 PC·같은 Windows 사용자 세션의 PowerShell에서 실행 중인지 확인하세요. 다른 PC나 WSL에서는 이 Windows 실행 파일이 자동으로 공유되지 않습니다. `status` 출력에는 로컬 브리지 키가 있으므로 문제 해결을 위해 공유할 때도 그대로 붙여 넣지 마세요.

또 다른 터미널에서 `status`를 실행하면 현재 로그인 계정과 **로컬 브리지 키**가 표시됩니다. 이 키는 같은 PC에서 ARTEX만 브리지에 접근시키는 비밀값입니다. 화면 공유·로그·Git에 붙이지 마세요.

```powershell
& "$env:LOCALAPPDATA\ARTEX\chatgpt-plan-bridge.exe" status
```

ARTEX의 **시스템 → LLM → 새 설정**에서 다음처럼 추가합니다.

| 항목 | 값 |
| --- | --- |
| 형식 | `OpenAI (Responses API)` |
| 주소 | `http://127.0.0.1:18789/v1` |
| API 키 | `status`에 표시된 *로컬 브리지 키* (OpenAI API 키가 아님) |
| 모델 | ARTEX의 모델 목록 새로고침에서 표시되는 계정별 모델 |
| 스트리밍 | **켜기** |
| 출력 토큰 상한 | `0` (구독 경로는 `max_output_tokens`를 허용하지 않음) |

설정을 저장하고 **연결 테스트**를 먼저 실행하세요. 그 후 원하는 새 작업에만 이 프로필을 지정합니다. 기존 진행 중인 작업의 모델은 자동으로 바뀌지 않습니다. 유료 API 프로필이 LLM 풀이나 작업별 대체 순서에 남아 있다면, 한도 초과 시 의도치 않게 API 요금이 발생할 수 있으므로 비활성화하거나 제외하세요.

연결 테스트에서 `invalid local bridge key`가 나오면 프로필 편집창의 **API 키** 입력칸에 브라우저가 다른 비밀번호를 자동 입력했는지 확인하세요. 기존 프로필의 키를 유지하려면 그 칸을 완전히 비워 둔 채 테스트·저장해야 합니다. 최신 화면은 로그인 비밀번호 자동완성을 억제하지만, 비밀번호 관리 도구가 별도로 값을 채운 경우에는 직접 지워야 합니다.

### 브리지 창이 닫혔거나 `Connected: false`가 나올 때

`serve`를 실행한 창이 사라져도 브리지 프로세스가 남아 있을 수 있습니다. 먼저 **브리지를 실행한 PC의 PowerShell**에서 `status`를 다시 확인하세요. `status`의 키 전체를 채팅, 이슈 또는 로그에 게시하지 마세요. 다른 실행 환경에서 같은 사용자 이름·PC 이름·경로가 보여도 인증 파일의 내용이 같다고 단정하지 말고, 실제 브리지를 실행하는 PowerShell의 결과를 기준으로 삼으세요.

```powershell
& "$env:LOCALAPPDATA\ARTEX\chatgpt-plan-bridge.exe" status
Get-CimInstance Win32_Process -Filter "name='chatgpt-plan-bridge.exe'" |
  Select-Object ProcessId,ExecutablePath,CommandLine
```

남은 프로세스의 `ExecutablePath`가 위 브리지 실행 파일이고 `CommandLine`이 `serve`인 것을 확인한 경우에만, 표시된 **해당 PID 하나**를 종료합니다. 아래 `PID`는 실제 조회된 번호로 바꾸세요. 다른 프로그램이나 ARTEX 서버를 종료하지 마세요.

```powershell
Stop-Process -Id PID
& "$env:LOCALAPPDATA\ARTEX\chatgpt-plan-bridge.exe" login
& "$env:LOCALAPPDATA\ARTEX\chatgpt-plan-bridge.exe" serve
```

`login`은 브라우저 승인을 마칠 때까지 기다린 뒤 실행하세요. `serve`는 실행된 채로 두고, **다른 PowerShell 창**에서 `status`가 `Connected: true`인지 확인합니다. 이후 ARTEX의 연결 테스트를 다시 실행하세요. `401 invalid local bridge key`가 계속되면, 그 PowerShell의 `status`에 표시된 키와 ARTEX LLM 프로필에 저장된 키를 로컬에서 맞추세요. 키는 브리지를 단순히 다시 실행한다고 매번 바뀌는 것은 아닙니다.

## 보안과 한도

- OAuth 액세스·갱신 토큰은 사용자 설정 폴더의 Windows DPAPI 암호화 파일에만 저장됩니다. Git·ARTEX DB·브라우저 저장소에 저장하지 않습니다.
- 브리지는 `127.0.0.1`에서만 수신하고 로컬 브리지 키가 없으면 요청을 거부합니다.
- 모델 목록은 로그인 계정이 노출할 수 있는 모델만 보여주며, 최종 사용 가능 여부는 실제 완료된 추론으로 확인합니다.
- Plus/Pro의 구독 사용량과 한도는 ChatGPT 설정의 사용량 페이지에서 확인합니다. 브리지의 실패가 자동으로 유료 API 결제를 승인하는 것은 아닙니다.
- 연결을 끊으려면 `chatgpt-plan-bridge.exe logout`을 실행합니다. 갱신 토큰을 취소하고 로컬 토큰을 지웁니다. 이후 ARTEX에서 해당 프로필을 비활성화하세요.
- 현재는 **한 번에 한 ChatGPT 계정**을 연결하는 로컬 설치 기능입니다. Pro에서 Plus로 같은 계정의 요금제만 변경하는 경우에는 재설치가 필요 없습니다.

공식 자료: [오픈소스 로그인](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), [모델·추론](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference), [현재 제한](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations).
