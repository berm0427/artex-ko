# v34 실제 ARTEX 긴-컨텍스트 루프 평가 절차 (설계, 2026-10-27)

## 0. 이 문서의 위치
`finding_v32_introspection_loop_20261026.md`가 #727의 근본 원인을 **compaction 요약
실패 + 행동 미실행**으로 확정했다. v33/v34 작업은 그 두 모드를 **짧은 holdout**에서
기계적으로 교정했다(v34: 확장 holdout 18/18, baseline v5: 13/18). 그러나 짧은
holdout은 실제 실패가 일어나는 **긴 컨텍스트**를 재현하지 못한다. 이 문서는 v34가
*실제 ARTEX 루프*에서 #727 같은 무한 반복을 푸는지 검증하는 절차를 **실행 전에**
고정한다(frozen protocol). 실행 결과는 별도 review 파일에 남긴다.

이 문서는 설계만 담는다. 실행은 (a) 라이브 ARTEX + Postgres + 격리 대상, (b)
사용자 승인(프로필 등록은 DB 변경, 대상 실행은 범위 승인)이 있어야 시작한다.

## 1. 가설 (frozen)
- H1(모델): v34는 built-in compaction 턴에서 9섹션 요약을 만들고 지시문을 반복하지
  않는다 → 작업 기억이 유지돼 오리엔테이션 루프를 벗어난다.
- H2(설정 공동원인): 출력 상한(ARTEX 512 / serve 768)을 올리는 것만으로도 절삭이
  줄어 루프가 완화된다. 따라서 v34의 순효과는 상한을 **동일하게 올린** 조건끼리
  비교해야 드러난다.

## 2. 성공/실패 기준 (frozen, 실행 전 고정)
한 번의 run을 다음으로 판정한다. (1차 지표는 기계 측정, 2차는 사람 검토.)

1차(기계, serve body-dump에서 산출):
- **compaction 성공률**: 그 run의 built-in compaction 턴들에 `eval_agentic_v33.py`의
  compaction 규칙(9섹션·지시문 미복제·비반복·비절삭)을 그대로 적용한 통과 비율.
  기준: ≥ 0.8.
- **행동 실행**: 첫 compaction 이후 action 도구(add_intent/read_file/exec/connect)
  호출이 **0이 아니고**, 조회 전용(list_*/graph_overview) 턴 비율이 단조 감소.
  기준: 첫 compaction 후 10턴 내 action ≥ 1.
- **루프 탈출**: 입력 토큰 sawtooth(압축→재증가)의 **동일 오리엔테이션 사이클
  반복이 3회 미만**에서 멈추고 frontier가 전진(열린 intent 생성·완료 발생).
- **전진 증거**: run 종료 시 완료된 intent ≥ 1, 또는 새 사실/파일 읽기로 상태가
  진전(반복이 아닌 신규 관찰).

2차(사람): 위 전진이 **외부 개입 없이** 자율로 일어났는가. (finding 메모: v5의
2h47m "성공"은 중간에 답을 던져준 결과라 자율 성공이 아니다 → 개입 run은 무효.)

판정:
- **reproduce**: 조건 A(아래)가 compaction 성공률 낮음 + 루프 지속을 보여야 실험이
  유효하다. A가 실패를 재현하지 못하면 이 대상은 테스트로 부적합 → 대상 교체.
- **promote 후보**: 조건 C(v34+상한↑)가 위 1차 기준을 모두 충족하고, 2차도 통과.
- 단 1개 대상은 일화적이다 → **서로 다른 대상 ≥ 3개**에서 재현돼야 결론으로 쓴다.

## 3. 비교 조건 (ablation — 모델 효과와 상한 효과 분리)
GPU가 1개(8GB)라 동시 서빙 불가 → **순차 실행**, 같은 대상·같은 초기 입력.

| 조건 | 어댑터 | serve `--max-output-tokens` | ARTEX 프로필 `max_tokens` | 목적 |
|---|---|---|---|---|
| A | adapter-12-four-task-v5 | 768 | 512 | #727 실패 **재현**(baseline) |
| B | adapter-12-four-task-v5 | 1536 | 1536 | 상한만 올림(H2 격리) |
| C | **adapter-agentic-v34** | 1536 | 1536 | **후보** |
| D(선택) | adapter-agentic-v34 | 768 | 512 | 모델만, 상한 그대로 |

해석: A가 실패·C가 성공이면 "모델+상한 합동 효과". B로 상한 단독 기여를, D로 모델
단독 기여를 분리한다.

## 4. 설정 (knobs — 코드에서 확인된 실제 위치)
1. **어댑터 서빙** (WSL): `scripts/start-learning-adapter.ps1`를 조건별 인자로 기동.
   - 어댑터: `learning/artifacts/adapter-agentic-v34`(C/D) 또는 `...four-task-v5`(A/B).
   - serve: `learning/serve_openai.py --max-output-tokens {768|1536} --max-input-tokens 32768 --port {고유}`.
   - 포트는 조건마다 분리(예: A=12360…D=12363), 모델명도 분리해 프로필 혼선 방지.
2. **ARTEX 프로필 등록** (opt-in, DB 변경): `learning/register-profile.ps1`.
   - SQL에 `max_tokens`가 **512로 하드코딩**돼 있음 → B/C용은 1536으로 바꾼 사본으로
     등록(또는 파라미터화). `is_default=false, pool_exclude=true` 유지(기본 풀 오염 금지).
   - `-Port`/`-ModelName`을 해당 조건 serve에 맞춘다. `context_window_k`는 대상에 맞게.
3. **출력 상한 2곳을 반드시 함께** 올린다: serve(`--max-output-tokens`)와 ARTEX
   프로필(`max_tokens`). 둘 중 하나만 올리면 `min(요청, 서버상한)`에서 다시 잘린다
   (serve 코드: `limit = min(requested, max_output_tokens)`).
4. **compaction 경로 고정**: built-in compaction만 사용(= #727 경로). noa 실험
   compaction은 off 유지(`agent/chat.go`의 noa resolver 미설정). `compaction.go`의
   백그라운드 compactor(MaxTokens 1500)는 별개이므로 혼동 금지.
5. **계측(필수)**: serve가 `/v1/chat/completions`의 요청 messages·tools, 응답 content,
   finish_reason, prompt/completion 토큰 수를 **턴별 jsonl로 덤프**해야 한다
   (v32의 `serve_debug.jsonl`과 동형). `serve_openai.py`엔 이미 `--debug-dump PATH`
   (+ `--condition-label`) 옵션이 구현돼 있으므로 그것을 사용한다. 이 덤프가 1차
   지표의 유일한 근거다.

## 5. 대상 (authorized, 격리 로컬만)
- 조건: 컨텍스트가 compaction 임계(관측상 입력 ~15k 토큰, ~16분 주기)까지 자라는
  **다단계** 작업. 단순 과제는 compaction을 안 쳐서 테스트가 안 된다.
- 1순위: #727을 일으킨 **로컬 Redis 실습**(또는 동등 재구성). finding이 이 작업에서
  루프를 관측했으므로 재현 가능성이 높다.
- 추가 2개: 서로 다른 유형(웹 정적 분석형 1 + 바이너리 정적 분석형 1)에서 각각
  긴 작업. Google CTF 2024(train split, Apache-2.0)나 다른 **허가된** 격리 대상만.
- 금지: 원격 요청, 미승인 Dreamhack/LINE 자료, flag/목표 문자열의 로그 저장.

## 6. run 실행
각 조건·대상마다:
1. 해당 serve 기동 → `/health` 확인. 다른 조건 serve는 내려 GPU 독점.
2. 해당 프로필을 ARTEX에서 **명시 선택**(기본 풀에 넣지 않음).
3. 작업을 생성하고 ARTEX를 **자율로** 실행. **중간 개입·힌트 금지**(개입 시 그 run
   무효 표시). 벽시계 예산 고정(예: 90분) 또는 종료조건(완료/명백한 정지) 도달까지.
4. 종료 후 serve 내리고 body-dump·ARTEX activity 로그를 보존(§8).

## 7. 측정·분석
- body-dump에서 **built-in compaction 턴 선별**: system == "You are a helpful AI
  assistant tasked with summarizing conversations." 인 요청.
- 각 compaction 턴을 `eval_agentic_v33.py`의 compaction 채점 함수로 평가 →
  성공률·절삭(finish_reason=length)·반복·섹션 수 집계.
- **턴 타임라인**: 도구 호출을 종류별(list_*/graph_overview vs action)로 시계열
  집계. 입력-토큰 sawtooth와 "오리엔테이션 사이클" 반복 횟수 산출(finding의
  `graph_overview→list_*→요약→` 패턴 매칭).
- **지표 산출**: §2의 1차 기준 네 가지를 조건×대상 표로. 2차(자율성)는 사람이 로그로.
- `eval_agentic_v33.py`는 입력을 서버에 보내는 모드지만, compaction 채점 로직
  (`grade_compaction`)은 응답 텍스트만 받으므로 body-dump의 저장 응답에 그대로 재사용
  가능(작은 어댑터 스크립트로 jsonl→채점).

## 8. 산출물·출처
- 이 문서를 `_frozen`으로 복사해 run 전 커밋(기준 고정 증명). 조건별: 어댑터 sha256,
  serve 인자, 프로필 SQL(비밀 제외), 대상 식별자, 시작/종료 시각 기록.
- 원시 로그(body-dump, ARTEX activity)는 대상 데이터가 들어갈 수 있으므로 **git 밖**
  `private/artex-learning/v34-long-loop/`에 보관(README의 기존 규율과 동일).
  저장소에는 집계 표와 review .md만 남긴다. flag/목표 문자열은 저장하지 않는다.

## 9. 한계 (정직하게)
- **일화성**: 대상 1개 결과는 결론이 아니다. 최소 3개 서로 다른 대상에서 재현 필요.
- **비결정성**: 디코딩은 greedy(do_sample=False)로 결정적이나, ARTEX 도구·타이밍·DB
  상태가 run마다 다르다 → 조건 간 비교는 **같은 대상·같은 시작 입력**으로 묶고,
  가능하면 조건당 2~3 run로 변동을 본다.
- **상한 교란**: A와 C는 상한이 다르다. B/D ablation 없이 "C가 A보다 낫다"만으론
  모델 기여를 못 가린다 → B/D를 반드시 포함.
- **holdout↔라이브 간극**: 짧은 holdout 통과가 긴 루프 성공을 보장하지 않는다.
  이 절차는 그 간극을 메우려는 것이지, holdout 결과로 대체하지 않는다.
- **compaction 다중 경로**: built-in만 측정한다. noa/백그라운드 compactor가 켜지면
  별도 경로라 혼입 금지(설정에서 확인).
- **승인**: 프로필 등록은 사용자 DB를 바꾸고, 대상 실행은 범위 승인이 필요하다.
  둘 다 사용자 확인 후에만 진행한다.

## 10. 내가 바로 할 수 있는 것 / 승인이 필요한 것
- 승인 없이(제안 시): body-dump→채점 어댑터 스크립트 작성, 집계·표 생성기 작성
  (`serve_openai.py`의 `--debug-dump`는 이미 구현돼 있음).
- 사용자 승인 필요: 프로필 등록(DB 변경), 라이브 ARTEX run(대상 범위), 원시 로그 보관.
