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

다음 명령으로 별도 터미널에서 로컬 브리지를 켭니다. ARTEX가 실행 중이어도 설정 전까지 기존 작업에는 영향이 없습니다.

```powershell
& "$env:LOCALAPPDATA\ARTEX\chatgpt-plan-bridge.exe" serve
```

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

## 보안과 한도

- OAuth 액세스·갱신 토큰은 사용자 설정 폴더의 Windows DPAPI 암호화 파일에만 저장됩니다. Git·ARTEX DB·브라우저 저장소에 저장하지 않습니다.
- 브리지는 `127.0.0.1`에서만 수신하고 로컬 브리지 키가 없으면 요청을 거부합니다.
- 모델 목록은 로그인 계정이 노출할 수 있는 모델만 보여주며, 최종 사용 가능 여부는 실제 완료된 추론으로 확인합니다.
- Plus/Pro의 구독 사용량과 한도는 ChatGPT 설정의 사용량 페이지에서 확인합니다. 브리지의 실패가 자동으로 유료 API 결제를 승인하는 것은 아닙니다.
- 연결을 끊으려면 `chatgpt-plan-bridge.exe logout`을 실행합니다. 갱신 토큰을 취소하고 로컬 토큰을 지웁니다. 이후 ARTEX에서 해당 프로필을 비활성화하세요.
- 현재는 **한 번에 한 ChatGPT 계정**을 연결하는 로컬 설치 기능입니다. Pro에서 Plus로 같은 계정의 요금제만 변경하는 경우에는 재설치가 필요 없습니다.

공식 자료: [오픈소스 로그인](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), [모델·추론](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference), [현재 제한](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations).
