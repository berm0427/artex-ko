# v34 계측 구현 메모 (2026-10-27)

`agentic_v34_long_loop_protocol.md`는 실행 전 고정한 설계 문서이므로 수정하지 않았다.
현재 구현은 해당 문서가 제안한 원문 body-dump 대신 **원문 없는 메타데이터**를
기록한다. 임의 목표 문자열은 정규식으로 완전히 가릴 수 없기 때문이다.

## 구현

- `serve_openai.py --debug-dump PATH --condition-label A`는 PATH가 저장소와
  OneDrive/Dropbox 밖일 때만 사용한다. 미지정 시 로그를 남기지 않는다.
- 200 응답과 반복·불완전 호출 422에는 토큰 수, finish reason, 도구 분류,
  compaction/action 채점 결과만 기록한다. 400/413/500은 상태와 원인 종류만
  기록한다. 요청·응답 본문과 도구 인자는 기록하지 않는다.
- `grade_compaction`과 `grade_action`은 응답이 메모리에 있을 때 기존 평가 함수로
  계산한다. 원문은 JSONL에 넣지 않는다.
- `grade_long_loop.py TRACE`는 compaction 통과율, 첫 compaction 다음 10턴 내
  조회 외 도구 호출, 413/422/5xx 수를 요약한다.
- `watch_long_loop.py TRACE`는 새 JSONL 이벤트와 누적 지표를 터미널에 즉시
  표시한다. `--once`로 현재 상태만 볼 수도 있다.

## 실시간 보기

감시기는 모델보다 먼저 실행해도 된다. 실제 v34 모델은 **별도 포트**를 쓰되,
GPU 메모리가 하나의 모델만 수용한다면 현재 서빙 중인 모델을 계획적으로 내린 뒤
시작한다. 현재 12346의 v32 서버는 이 준비 단계에서 건드리지 않았다.
Windows에서 WSL의 새 포트로 접속하려면 해당 포트의 전달 설정도 확인해야 한다.
아래 경로와 명령은 이후 실행용 예시다.
메타데이터 파일의 부모 폴더는 미리 만들고, 저장소·OneDrive·Dropbox 밖에 둔다.

먼저 `& .\scripts\check-v34-readiness.ps1`로 프로필·서버 모델 정체성,
어댑터 파일, WSL 런타임, 경로 및 포트 전달을 읽기 전용으로 확인한다.
2026-10-27 점검에서는 12346 서버가 v32인데 해당 주소의 프로필은 v5였고,
v34 프로필과 12362 전달도 없었다. 이 불일치를 해소하기 전의 run은 v34
평가로 인정하지 않는다.

```powershell
$trace = 'C:\Users\berm0\Documents\Codex\private\artex-learning\v34-long-loop\condition-C.jsonl'
New-Item -ItemType Directory -Path (Split-Path -Parent $trace) -Force | Out-Null
& .\scripts\start-learning-adapter.ps1 -Port 12362 `
  -AdapterPath 'learning\artifacts\adapter-agentic-v34' `
  -ModelName 'artex-agentic-v34' -MaxOutputTokens 1536 `
  -DebugDump $trace -ConditionLabel C
python .\learning\watch_long_loop.py $trace `
  --expected-model artex-agentic-v34 --expected-condition C
```

ARTEX 프로필 등록 시에는 서버 상한과 프로필 상한을 함께 지정한다. 이 명령은
DB를 변경하므로 실제 시험을 시작할 때만 실행한다.

```powershell
& .\learning\register-profile.ps1 -Port 12362 -ProfileName 'ARTEX v34 C' `
  -ModelName 'artex-agentic-v34' -ContextWindowK 32 -MaxTokens 1536
```

작업 ID가 정해졌다면 별도 터미널에서 ARTEX 상태도 볼 수 있다.

```powershell
& .\scripts\show-task-progress.ps1 -TaskId <ID> -Watch
```

서빙 계측 파일은 모델 요청 상태만 보여 준다. ARTEX 작업의 실제 완료 여부는
두 번째 명령의 작업 기록 및 원시 대상 응답으로 판단한다.
실험 후에는 아래 명령으로 모델·조건 일치를 재검증한다.

```powershell
python .\learning\grade_long_loop.py $trace `
  --expected-model artex-agentic-v34 --expected-condition C
```

## 남은 증거

- 원문 없는 메타데이터만으로 요약의 의미적 충실도, 동일 오리엔테이션 사이클의
  탈출, 실제 intent 완료를 판정할 수 없다. 이 항목은 ARTEX activity/DB의
  허가된 읽기 전용 증거와 사람 검토가 필요하다.
- 여기까지는 CPU 단위 테스트만 수행했다. v34 모델 서버·ARTEX 라이브 작업은
  이 구현 단계에서 실행하지 않았다.
