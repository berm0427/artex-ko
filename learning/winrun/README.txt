ARTEX 학습 파이프라인 — Windows 실행용 (WSL은 안에서 자동 호출)
================================================================

이 폴더의 .bat 파일을 Windows 탐색기에서 더블클릭하거나, cmd/PowerShell에서
실행하면 됩니다. WSL(Ubuntu)에 직접 들어갈 필요가 없습니다 — 각 .bat이
wsl.exe를 통해 안에서 venv 파이썬을 호출합니다.

전제
----
- WSL2 + Ubuntu가 설치되어 있고(이미 설치됨), venv가 존재:
    /home/c0d1n9/.venvs/artex-qlora-check/bin/python  (torch+CUDA)
- 베이스 모델:
    C:\Users\berm0\Documents\Codex\models\Qwen3-4B-unsloth-bnb-4bit
- GPU는 1장(8GB) → 서빙/평가는 한 번에 하나만.

순서
----
1-gen-data.bat        학습셋(v2) + 홀드아웃(v33) 재생성 (빠름, 결정적)
2-train-v34.bat       v34 어댑터 학습 (몇 분, GPU)
3-serve-v34.bat       v34 서빙 (창 유지; 직접 호출해 보고 싶을 때)
                      health: http://127.0.0.1:12354/health
4-eval-v34.bat        v34 서빙+홀드아웃 채점+종료 → learning\eval_v33_candidate_v34.jsonl
5-eval-baseline-v5.bat 비교용 baseline 채점 → learning\eval_v33_baseline_v5.jsonl

보통은 2 → 4(→ 5로 비교)만 돌리면 됩니다. 데이터를 바꿨으면 1부터.

채점 기준
---------
- compaction: <analysis>+<summary> 9섹션, 지시문 미복제, 비반복, 비절삭
- action    : 열린 intent가 없을 때 add_intent 호출 (list_* 반복/422 아님)

참고
----
- 로직은 같은 폴더의 .sh 파일에 있고, .bat은 그걸 WSL로 넘기는 얇은 래퍼입니다.
- 출력 상한은 서빙/평가 모두 1536으로 맞춰 절삭 교란을 제거합니다.
- 서빙 로그: .runtime\agentic-v33\serve_<model>.log
- 플래그를 바꾸려면 해당 .sh만 고치면 됩니다(.bat은 그대로).
