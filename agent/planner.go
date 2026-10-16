package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Planner is the event-driven LLM planner (docs §4.3): each time the asset or
// exploration graph changes (debounced), it reads the exploration route, queries
// assets, judges whether the task goal is met, and emits 0..N exploration intents
// into the frontier. It is the sole intent generator.
type Planner struct {
	findingRecorder   FindingRecorder
	prov              llm.Provider
	model             string
	tx                *transcript.Store                      // raw LLM conversation persistence (nil = off)
	window            int                                    // context window in tokens (for compaction)
	windowFn          func() int                             // optional dynamic task-chain minimum
	maxTurns          int                                    // max agent turns per run (0 = unlimited)
	killWork          func(intentID int64) error             // engine callback to terminate a running work (nil = off)
	steerWork         func(intentID int64, msg string) error // engine callback to steer a running work mid-run (nil = off)
	proxyAddr         string                                 // recording proxy for WebFetch (empty = direct)
	proxyCACert       string                                 // recording proxy's CA cert path (HTTPS verify)
	webSearch         WebSearchOpts                          // web_search tool backend selection (off by default)
	workDir           string                                 // shared work dir (surfaced in prompt as artifact-output target)
	injectConstraints func() bool                            // resolver: inject task operation constraints into system prompt? (nil = yes)
	nonStreamingFn    func() bool                            // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn      func() bool                            // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn       func() int                             // resolver: per-reply output cap (nil/0 = send no cap)
	compactor         *Compactor                             // cold-node compaction (§7); nil = disabled

	// todos keeps ONE plan-scratchpad per task (keyed by exploration id) so the
	// planner's multi-step plan survives across wake-ups — each Plan() is a fresh
	// session, but the shared store lets it record a serial exploit chain once and
	// dispatch it step-by-step over rounds instead of front-loading it in parallel.
	todoMu sync.Mutex
	todos  map[int64]*actool.TodoStore
}

func NewPlanner(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *Planner {
	return &Planner{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns, todos: map[int64]*actool.TodoStore{}}
}

func (p *Planner) SetCompactionWindowResolver(fn func() int) { p.windowFn = fn }

// SetCompactor wires the cold-node compactor (cold-digest §7). Called each
// planner wake-up to advance the round counter, maintain cold stamps, and
// (off the hot path) fold cold nodes into digests. nil = feature disabled.
func (p *Planner) SetCompactor(c *Compactor) { p.compactor = c }

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default).
func (p *Planner) SetNonStreaming(fn func() bool) { p.nonStreamingFn = fn }

func (p *Planner) nonStreaming() bool { return p.nonStreamingFn != nil && p.nonStreamingFn() }

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (p *Planner) SetNoaEnabled(fn func() bool) { p.noaEnabledFn = fn }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (p *Planner) SetMaxTokens(fn func() int) { p.maxTokensFn = fn }

func (p *Planner) maxTokens() int {
	if p.maxTokensFn == nil {
		return 0
	}
	return p.maxTokensFn()
}

func (p *Planner) compactionWindow() int {
	if p.windowFn != nil {
		return p.windowFn()
	}
	return p.window
}

// SetProxy points the planner's WebFetch at the recording proxy plus the CA cert
// it trusts to verify HTTPS through it (empty addr = direct).
func (p *Planner) SetProxy(addr, caCert string) { p.proxyAddr, p.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for the planner (off by default).
func (p *Planner) SetWebSearch(o WebSearchOpts) { p.webSearch = o }

// SetConstraintInject wires a resolver deciding whether this task's operation
// constraints get injected into the planner system prompt. Read per round so the
// settings toggle takes effect without rebuilding the agent. nil = inject (default).
func (p *Planner) SetConstraintInject(fn func() bool) { p.injectConstraints = fn }

// wantConstraints reports whether constraint injection is enabled (default yes).
func (p *Planner) wantConstraints() bool { return p.injectConstraints == nil || p.injectConstraints() }

// todoFor returns the task's persistent planning todo store, creating it on first
// use. Shared across all of this task's planner wake-ups.
func (p *Planner) todoFor(expID int64) *actool.TodoStore {
	p.todoMu.Lock()
	defer p.todoMu.Unlock()
	s := p.todos[expID]
	if s == nil {
		s = actool.NewTodoStore()
		p.todos[expID] = s
	}
	return s
}

// SetKillWork wires the engine's per-work terminate callback so the planner's
// kill_work tool can stop a single running worker.
func (p *Planner) SetKillWork(fn func(intentID int64) error) { p.killWork = fn }

// SetSteerWork wires the engine's per-work steering callback so the planner's
// steer_work tool can inject a mid-run course-correction into a running worker.
func (p *Planner) SetSteerWork(fn func(intentID int64, msg string) error) { p.steerWork = fn }

// renderPlannerTodos formats the persistent planning todo for injection into the
// wake-up prompt (empty when there are no todos yet — first wake-up).
func renderPlannerTodos(items []actool.Todo) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【이전 회차에서 작성한 계획 할 일(다음 회차에도 유지됨)】: \n")
	for _, it := range items {
		mark := map[actool.TodoStatus]string{actool.TodoPending: "☐", actool.TodoInProgress: "▶", actool.TodoCompleted: "✔"}[it.Status]
		if mark == "" {
			mark = "☐"
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", mark, it.Content))
	}
	b.WriteString("이 목록을 바탕으로 선행 단계가 완료되었거나 필요한 fact가 존재하는 다음 단계에만 의도를 배정하세요. TodoWrite로 충족된 단계를 completed로 갱신하고 pending/in_progress 단계는 중복 배정하지 마세요.")
	return b.String()
}

// TriggerEvent describes what concretely caused this planning round to fire, so
// the planner looks first at the actual change instead of re-scanning the whole
// overview. Kind:
//
//	"done"    — a worker finished intent IntentID (its output conclusion is fetched).
//	"finding" — a worker reported a finding on intent IntentID (Detail = 摘要).
//	"goal"    — the human (via 主 agent 的 set_goals) added one OR MORE goals in a
//	            single call (Goals = 本次新增的目标文本，1+ 条；set_goals 支持批量).
//	"goal_deleted" — the human deleted a goal from 总览的目标管理 (Detail = 被删目标文本).
//	"goal_edited"  — the human edited a goal from 总览的目标管理 (OldGoal→NewGoal 文本).
//	"cancelled" — the human deleted intent IntentID (Detail = 删除原因). The intent is
//	            stopped (not deleted) and the reason is attached to it as a fact.
type TriggerEvent struct {
	Kind     string
	IntentID int64
	Detail   string
	Summary  string   // Kind=="cancelled" 专用：删除前捕获的意图摘要（真删除后节点已不存在，无法再查）
	Goals    []string // Kind=="goal" 专用：本次 set_goals 新增的目标文本（1 条或多条）
	OldGoal  string   // Kind=="goal_edited" 专用：修改前的目标文本
	NewGoal  string   // Kind=="goal_edited" 专用：修改后的目标文本
	Hints    []string // Kind=="hint" 专用：本次 add_hint 新增的提示文本（1 条或多条）
}

// renderTriggers spells out the change(s) that fired this round: for a finished
// worker — which intent + its output conclusion; for a finding — which intent +
// what was found. Empty for time/heartbeat wakes. Reads the store (best-effort;
// a blank field never blocks the round).
func renderTriggers(ts *db.ExplorationStore, evs []TriggerEvent) string {
	if len(evs) == 0 || ts == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【이번 회차를 일으킨 실제 변경 사항(먼저 확인한 뒤 새 방향 필요 여부 판단)】:")
	for _, ev := range evs {
		switch ev.Kind {
		case "goal":
			if len(ev.Goals) == 1 {
				b.WriteString(fmt.Sprintf("\n- 사용자(메인 에이전트)가 목표를 추가했습니다: %s — 해당 의도가 없으면 탐색 방향을 보완하세요.", ev.Goals[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- 사용자(메인 에이전트)가 목표 %d개를 추가했습니다: %s — 의도가 없는 목표마다 탐색 방향을 보완하세요.", len(ev.Goals), strings.Join(ev.Goals, "; ")))
			}
		case "hint":
			if len(ev.Hints) == 1 {
				b.WriteString(fmt.Sprintf("\n- 사용자(메인 에이전트)가 전략 힌트를 추가했습니다: %s — 탐색 그래프에 연결되었으니 필요하면 방향을 조정·보완하세요.", ev.Hints[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- 사용자(메인 에이전트)가 전략 힌트 %d개를 추가했습니다: %s — 각각에 따라 탐색 방향을 조정·보완하세요.", len(ev.Hints), strings.Join(ev.Hints, "; ")))
			}
		case "goal_deleted":
			b.WriteString(fmt.Sprintf("\n- 사용자가 목표를 삭제했습니다: %s — 남은 목표와 방향을 다시 판단하고 삭제된 목표에는 의도를 배정하지 마세요.", ev.Detail))
		case "goal_edited":
			b.WriteString(fmt.Sprintf("\n- 사용자가 목표를 '%s'에서 '%s'(으)로 수정했습니다. 새 목표에 맞춰 방향을 조정하세요.", ev.OldGoal, ev.NewGoal))
		case "finding":
			b.WriteString(fmt.Sprintf("\n- 의도 #%d(%s)의 실행 에이전트가 취약점을 보고했습니다: %s", ev.IntentID, intentSummary(ts, ev.IntentID), ev.Detail))
		case "cancelled":
			// 意图内容优先用删除时捕获的 Summary（真删除后节点已不存在，intentSummary 查不到）。
			sm := ev.Summary
			if sm == "" {
				sm = intentSummary(ts, ev.IntentID)
			}
			b.WriteString(fmt.Sprintf("\n- 사용자가 의도 #%d를 삭제했습니다. 내용: %s, 이유: %s. 더는 실행되지 않으니 다시 계획하세요.", ev.IntentID, sm, ev.Detail))
		default: // "done"
			b.WriteString(fmt.Sprintf("\n- 의도 #%d(%s)의 실행 에이전트가 종료되었습니다. 결론: %s", ev.IntentID, intentSummary(ts, ev.IntentID), workerOutput(ts, ev.IntentID)))
			if fids := factIDsYielded(ts, ev.IntentID); fids != "" {
				b.WriteString(fmt.Sprintf("; 이 의도에서 새로 나온 사실 ID: %s ", fids))
			}
		}
	}
	b.WriteString("\n(전체 내용은 node_detail/get_worker_output/list_findings로 다시 조회할 수 있습니다.)")
	return b.String()
}

// factIDsYielded lists the fact ids an intent produced this run as "#12、#15", so the
// planner can jump straight to the round's incremental facts. Empty (best-effort) when
// the intent yielded no facts or the lookup fails.
func factIDsYielded(ts *db.ExplorationStore, id int64) string {
	ids, err := ts.FactsYielded(id)
	if err != nil || len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, fid := range ids {
		parts[i] = fmt.Sprintf("#%d", fid)
	}
	return strings.Join(parts, "、")
}

// intentSummary reads an intent node's one-line summary (best-effort, "?" on miss).
func intentSummary(ts *db.ExplorationStore, id int64) string {
	n, err := ts.GetNode(id)
	if err != nil || n == nil {
		return "?"
	}
	var p map[string]any
	if json.Unmarshal(n.Payload, &p) == nil {
		if s, ok := p["summary"].(string); ok && s != "" {
			return s
		}
	}
	return "?"
}

// workerOutput returns the finished worker's conclusion for an intent — the last
// 'result' (else 'text') activity's full detail, truncated. Same source get_worker_output uses.
func workerOutput(ts *db.ExplorationStore, id int64) string {
	acts, _, err := ts.ActivityList(&id, 0, 1000)
	if err != nil {
		return "(출력 조회 실패)"
	}
	var pick *db.Activity
	for i := range acts {
		if acts[i].Kind == "result" {
			pick = &acts[i]
		} else if acts[i].Kind == "text" && pick == nil {
			pick = &acts[i]
		}
	}
	if pick == nil {
		return "(이 의도의 출력 기록 없음)"
	}
	out, _ := ts.ActivityDetail(pick.ID)
	if out == "" {
		out = pick.Summary
	}
	return truncOutput(out, 800)
}

// truncOutput caps a worker-output blob so the trigger context doesn't bloat the
// system prompt every round; full text is one get_worker_output call away.
func truncOutput(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + " …(생략됨; 전체 내용은 get_worker_output 참조)"
}

// renderGraphOverview folds the pre-computed graph_overview snapshot into the
// wake-up prompt so the planner starts each round with the full situation in
// hand — saving the round-trip it would otherwise spend calling the tool. It is
// the exact same JSON graph_overview would return; deeper detail is still one
// tool call away (node_detail / list_facts / …).
func renderGraphOverview(data map[string]any) string {
	b, err := json.Marshal(data)
	if err != nil {
		return "" // fall back to the model calling graph_overview itself
	}
	return "\n\n【이번 회차 현황(graph_overview를 미리 조회한 결과; 상세 내용은 node_detail/list_facts 등으로 확인)】: \n" + string(b)
}

// plannerDefaultTmpl is the built-in EDITABLE body (段 [A]) of the planner prompt,
// seeded into agent_prompts. Goal is a {{.Goal}} template var; the 中间产物输出规约
// tail is code-owned (artifactSpec) and appended by plannerSystem after rendering.
const plannerDefaultTmpl = `당신은 승인된 보안 점검 시스템의 계획 에이전트입니다. 그래프가 바뀔 때마다 호출됩니다. 현황을 읽고 목표를 판정하며, 아직 다른 의도가 다루지 않는 새 방향이 있을 때만 탐색 의도를 추가하세요. 계획자는 실행자가 아닙니다. 이번 회차에는 의도를 정의하거나 목표를 판정할 뿐, 직접 분석·검증·이용하지 마세요.

작업 목표: {{.Goal}}

**이번 회차의 의도 생성 기준**
- **최우선 기준**: 목표가 아직 달성되지 않았고 open/running 의도가 전혀 없으면(frontier_open=0, running_intents 비어 있음) 목표로 나아가는 의도를 적어도 하나 생성하세요. 대기 중인 방향도 실행 중인 작업도 없는 상태에서 0개를 생성하면 작업이 멈춥니다. recent_done만 남았다면 아래의 상태별 기준으로 새 경로를 시작하거나 중단된 경로를 이어가세요.
- 그 밖에 0개는 정당한 경우에만 허용됩니다. 가능한 방향이 모두 open/running 의도로 이미 처리 중이거나, 다음 단계가 실행 중인 의도의 실제 결과에 의존한다면 기다리세요. 표현만 바꾼 중복 의도를 만들지 마세요.
- 아직 다루지 않았고 실행 중인 작업에 의존하지 않는 방향이나 범위 내 미검사 대상이 있다면 의도를 생성하세요.

**회차별 판단 절차**
1. 아래에 첨부된 현황은 graph_overview의 결과입니다. task, 자산 수, goals와 상태, open/running/recent_done 의도, sites_without_endpoints, facts와 recent_facts를 확인하세요. 별도 graph_overview 호출은 필요하지 않습니다.
   - 목표·의도·사실·취약점 탐색 노드는 이 작업에 속하지만, 자산 그래프는 모든 작업이 공유합니다. 다른 작업의 자산은 무시하세요.
   - 각 의도의 parents/yields와 사실의 from_intent로 상위 근거와 하위 결과를 연결하세요.
   - 포트 닫힘이나 주입 불가 같은 부정 관찰은 실행 에이전트의 관찰이지 자동 확정이 아닙니다. node_detail(id)로 증거를 읽으세요. 충분한 증거, confidence=observed, 합리적인 방법의 검토가 함께 있을 때만 일단 막힌 경로로 보세요. 증거가 없거나 단일 시도 또는 confidence=inferred라면 미확인으로 보고, 범위 내이며 다른 의도가 다루지 않을 때 복증 의도를 한 번 만들 수 있습니다. 동일한 부정 방향은 최대 한 번만 복증하고 합리적인 부정 증거가 나오면 존중하세요.
   - 추가 상세 내용이 필요할 때만 list_facts(기본 20개, q/before 페이지), list_findings, node_detail, list_assets(q/type/company_id/task_id/id/ids), asset_neighbors를 사용하세요. 공유 자산을 매번 전부 가져오지 마세요.

2. **목표 판정**: 발견·사실이 미달성 목표를 실제로 입증하면 prove_goal(goal_id, evidence_id, reason)으로 met 처리하세요. 마지막 목표가 입증되면 시스템이 작업을 자동 완료합니다. 별도의 일괄 완료 수단은 없습니다.
   - 목표에 정량 기준(커버리지 X%, 플래그 N개, 특정 권한 등)이 있다면 prove_goal 전에 graph_overview의 coverage.pct, findings_total 등 실제 값을 확인하세요. 미달이면 달성 처리하지 말고 부족한 부분을 검증할 의도를 만드세요. 일부 성과를 전체 달성으로 간주하지 마세요.

3. **선택적 초기 이해 탐지**: 작업 초기에 사실이 거의 없고 현황만으로 첫 의도를 구체화할 수 없을 때만 Bash 등으로 소량의 읽기 전용 탐지를 할 수 있습니다. 목적은 정확한 의도 한 문장을 만드는 것입니다. 취약점 발견·검증·이용이나 엔드포인트 열거는 실행 에이전트의 일입니다.
   - 이미 실행 에이전트가 남긴 사실이 있다면 직접 탐지하지 말고 그것을 바탕으로 새 의도를 만들거나 종료하세요.
   - 초기에도 탐지는 최대 3회입니다. 심층 조사나 개별 매개변수·엔드포인트 반복 확인을 시작했다면 즉시 멈추고 의도로 배정하세요.
   - 기존 정보로 판단 가능하면 탐지 자체가 필요 없습니다.

4. **새 방향 선정**: 절제는 기존 의도를 반복하지 않는다는 뜻이지 가능한 작업을 줄이라는 뜻이 아닙니다. 목표가 미달성이라면 더 깊고 본질적으로 다른 미검토 방향을 찾으세요. 의도는 고정 분류가 아닌 열린 탐색 방향입니다. open/running/recent_done과 비교하세요.
   - open/running이 이미 다루면 생성하지 마세요.
   - recent_done에 있으면 상태를 확인하세요. done은 정상 종료이므로 그대로 재배정하지 말고, yields의 사실을 검토해 실질적인 새 근거가 있을 때만 새 차이를 summary에 명시하세요. exhausted는 예산 소진, blocked는 모델·네트워크 실패 등으로 미완료일 수 있습니다. get_worker_trace/get_worker_output으로 실제 진행과 중단 원인을 확인한 뒤 이어서 진행·동일 방향 재시도·다른 방법 중 하나를 선택하세요. 상태값만으로 결론 내리지 마세요.
   - 아무 의도도 다루지 않는 새 방향은 생성하세요. 알려진 모든 방향이 open/running에서 처리 중이면 기다리되, 목표가 미달성이고 recent_done만 남았다면 최우선 기준에 따라 새 방향 또는 이어갈 방향을 마련하세요.
   - 커버리지는 최소 기준 또는 검수 기준이지 유일한 탐색 목표가 아닙니다. 실제로 가치 높은 진입점이 확인되면 단순히 자산을 얕게 넓히기보다 그 경로를 심층 검증하세요.
   - 한 경로에 너무 일찍 수렴하지 마세요. 범위 내에서 원리가 다른 미검토 진입점·자산·연쇄 경로가 있으면 보완하세요. 다만 **작업 제약 조건이 다양성보다 우선**입니다. 제외된 호스트·포트·행위에 대한 의도를 만들지 마세요.
   - **순차 의존 경로는 병렬로 쪼개지 마세요.** TodoWrite에 각 단계를 남기고 전제 조건이 충족된 한 단계만 배정하세요. 실제 fact가 나오면 다음 회차에 이어가세요. 같은 일을 표현만 달리해 두 개로 나누지 마세요. 독립적인 차원에만 병렬 의도를 사용하세요.

5. **제출**: add_intent를 한 번 호출해 가장 가치 높은 새 방향을 intents 배열로 최대 4개까지 제출하세요.
   - summary에는 대상의 전체 주소, 할 일, 이유를 자연어 한 문장으로 적으세요. 기존 의도와 비교해 중복을 제거하세요.
   - 구체적인 대상 자산이 있으면 list_assets에서 확인한 asset_ids를 전달하세요. 여러 자산이 관련되면 모두 넣고, 대상 자산이 없는 전역 정찰에만 비워 두세요.
   - 사실·의도·발견에서 파생된 방향이라면 관련 parent_ids를 전달하세요. 독립적인 최상위 새 방향만 비워 두세요.

중복 없이 목표에 필요한 방향을 정확하게 배정하세요.`

func plannerSystem(goal, dataDir, workDir string) string {
	body := renderSystem("planner", plannerDefaultTmpl, PlannerVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	body += "\n\n**미확인 입력 서비스의 후속 계획**: 서버가 `Input:` 같은 프롬프트만 보내고 이전 문자열 입력에 차이가 없었다면, `help`·`flag`·`status` 등 단어만 바꾼 의도를 새로 만들지 마세요. 기존 도구 출력에서 실제 전송 바이트·수신 바이트·연결 종료를 확인하고, 다음 의도에는 한 번에 한 변수만 바꾸는 입력 길이·종결 방식·동일 연결의 단계 변화처럼 이전 시도와 구별되는 검증 질문 및 관찰 기준을 명시하세요. 단순 무응답은 취약점 부재나 목표 달성의 증거가 아닙니다. 새로운 검증 질문을 만들 수 없다면 그 한계를 명확히 기록하세요."
	return body + artifactSpec(workDir) + evidenceLedMethodRule + serviceProtocolEvidenceRule + serviceProgressRule + hypothesisCoverageRule + platformShellDirective() + langDirective()
}

// Plan runs one planning round. emit, if non-nil, receives the planner's execution
// steps (so users can see how it reads the situation and judges goals — the
// planner is the intent generator and was previously a black box). Returns whether
// the planner judged the goal met.
// triggers carries the concrete change(s) that fired this round — worker(s) done
// and/or finding(s) reported (may be several — the engine debounces a burst; empty
// for time/heartbeat wakes). They are spelled out at the top of the prompt so the
// planner looks first at the actual change (which intent, its output/finding).
func (p *Planner) Plan(ctx context.Context, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, goal string, triggers []TriggerEvent, emit func(db.Activity)) (met bool, reason string, err error) {
	// cold-digest §2.3/§7: advance this task's planner-round counter, maintain the
	// cold_since_round stamps, and (if a threshold is hit) kick off background
	// compaction. Synchronous part is cheap (a few queries); the LLM compaction
	// runs in a detached goroutine so it never adds latency to this round.
	p.compactor.OnPlannerRound(ctx, ts)
	tsx := NewToolSet(ts, "planner")
	tsx.SetFindingRecorder(p.findingRecorder)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	tsx.killWork = p.killWork   // enable kill_work tool (nil = unavailable)
	tsx.steerWork = p.steerWork // enable steer_work tool (nil = unavailable)
	if origin, _ := ts.OriginFactID(); origin > 0 {
		tsx.SetOwnerNode(origin) // planner-side anchors default to the task root (origin fact)
	}
	// 领域工具 + 基础默认工具集（Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash）
	// 资产覆盖度功能关闭时剔除 add_task_scope/list_untested_assets（不入 prompt）。
	base := append(tsx.DropCoverageTools(tsx.PlannerTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "planner", base)
	defer cleanup()
	// 关键态势（刚完成的意图 + 预取的完整图）改放【本轮 user 输入】(见下方 input)，system
	// 只留静态规划正文。move-out 让 system 每轮稳定、更利于缓存；代价是若单轮变长，态势可能
	// 被 compaction 压缩（planner 单轮通常短，风险低）。situational 会拼进下方 input。
	situational := renderTriggers(ts, triggers) + renderGraphOverview(tsx.graphOverviewData()) + recentProgressAudit(ts)
	// 任务级 deadline / 终局模式(经 ctx 注入,见 taskclock.go)。终局那一轮把任务超时
	// planner 收尾词作为【本轮操作指令】拼进本轮 user 输入(随 situational),让它只做最后
	// 目标判定、不产新意图。
	tc := taskClockFrom(ctx)
	if tc.Final {
		situational += "\n\n【작업 종료 단계(이번 회차에만 적용하며 일반 계획 절차보다 우선)】: " + resolveTaskTimeoutWrapup("planner")
	}
	// 本任务的工作目录 <workDir>/tasks/<taskID>，先建好。
	taskDir := ensureRunDir(p.workDir, taskID, 0)
	ctx = intercept.WithReviewContext(ctx, taskDir, intercept.ReviewBackground{})
	sysBody := plannerSystem(goal, p.workDir, taskDir)
	if p.wantConstraints() {
		sysBody += constraintBlock(ts) // 操作约束(若有)注入系统提示,框定探索边界
	}
	system, boundary := deferredSystem(sysBody, def)
	// planner 无自身墙钟预算;有 deadline 时把 MaxDuration 夹逼到剩余,让在跑的规划轮在
	// 任务到点时进收尾(因超时→任务超时词,因步数→per-run 词)。
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, 0)
	settle := wrapupSettlement("planner", nil)
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("planner", nil, clamped)
	}
	opts := agentcore.Options{
		Provider:        p.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true, // 走记录代理留痕；载入代理 CA 验证 MITM 重签的 HTTPS 证书
		WebFetchProxy:   p.proxyAddr,
		WebFetchCACert:  p.proxyCACert,
		// 联网搜索(可选)。ddgs 无需 key；brave-free 需 BraveKey；tavily 需 TavilyKey。
		// WebSearchProxy 是独立出口代理(http/https/socks5)，与记录流量的 MITM 代理无关；空则直连。
		EnableWebSearch:       p.webSearch.Enabled,
		WebSearchBackend:      p.webSearch.Backend,
		BraveSearchAPIKey:     p.webSearch.BraveKey,
		TavilySearchAPIKey:    p.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: p.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  p.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   p.webSearch.DeepSeekModel,
		WebSearchProxy:        p.webSearch.Proxy,
		BashEnv:               proxyEnv(p.proxyAddr, p.proxyCACert), // Bash 子命令默认走代理+信任 CA
		WorkingDir:            taskDir,                              // 本任务工作目录 <workDir>/tasks/<taskID>
		ToolOutputDir:         cmdOutDir(taskDir),
		MaxTurns:              p.maxTurns, // 0 = unlimited (configurable in agent management)
		MaxDuration:           maxDur,     // 0=不限;有 deadline 时=距 deadline 剩余
		Compaction:            compactionConfig(p.compactionWindow()),
		// 跨唤醒共享的规划待办：让串行链在多轮之间保留（session 是新的，store 不是）。
		Todos: p.todoFor(ts.ID()),
		// 命中【本轮】步数预算→ SDK 跑收尾:把本轮已想清楚的结论落地(该派的 add_intent、
		// 能证的 prove_goal、串行链记 TodoWrite),而非停止规划——planner 之后仍会被反复唤醒。
		// clamped(被任务 deadline 夹逼)时改用 PromptByReason(见 wrapupSettlementForTask)。
		Settlement:   settle,
		NonStreaming: p.nonStreaming(), // 该 profile 选非流式时走 Provider.Complete
		MaxTokens:    p.maxTokens(),    // 0 = 不发上限,由服务端默认值决定
	}
	if p.tx != nil { // persist raw LLM conversation; one accumulating file per task's planner
		opts.Transcript = p.tx
		opts.SessionID = fmt.Sprintf("exp%d-planner", ts.ID())
	}
	// 实验功能:开启后由 noa 接管上下文压缩(归档集中在 <workDir>/noa/<SessionID> 下,持久)。
	noaSession := fmt.Sprintf("exp%d-planner", ts.ID())
	enableNoa(&opts, p.noaEnabledFn, p.workDir, noaSession, noaWarn(noaSession))
	// 态势（刚完成的意图 + 完整图）现在拼进本轮 user 输入（见下方 input）。user 里还有
	// 指令 + 跨唤醒待办（todo 是模型自己的规划便签，可再生，放 user 即可）。
	// 开场白按「本轮有无具体变动」分两种：有变动 → 指向下方【实际变动】块；无变动
	// (心跳定时巡检 / hint / 恢复等) → 别谎称"图发生了变化",转而提示顺带复查在跑意图。
	lead := "방금 구체적인 변경이 있었습니다. 아래의 【이번 회차를 일으킨 실제 변경 사항】을 읽고 다음 단계를 계획하세요:"
	if len(triggers) == 0 {
		lead = "이번 회차는 정기 점검 또는 구체적 변경 신호가 없는 호출입니다. 그래프가 변경되지 않았을 수 있습니다. 실행 중인 의도를 확인하고, 장시간 진전이 없거나 방향을 벗어났다면 steer_work로 조정하고 방향 전체가 잘못됐다면 kill_work를 검토하세요. 그런 다음 목표와 새 방향 필요 여부를 판단하세요:"
		// 心跳/无变动唤醒时,若全图已无任何 open 或 running 意图 → 探索已停摆(没 worker 在跑、
		// 也没排队方向)。明确告知 planner 并强制其本轮补出新方向,别只复查在跑意图后空转一轮。
		if active, err := ts.HasActiveIntent(); err == nil && !active {
			lead = "이번 회차는 정기 점검이며 현재 open/running 의도가 전혀 없습니다. 실행 중이거나 대기 중인 방향이 없어 탐색이 멈춘 상태입니다. 아래 현황에서 목표 달성 여부를 먼저 판단하세요. 목표가 미달성이면 기존 의도와 중복되지 않는 새 의도를 이번 회차에 하나 이상 생성해야 합니다:"
		}
	}
	input := lead + situational + "\n\n위 현황을 바탕으로 목표를 판정하세요. 성과나 취약점이 실제로 확인되어 목표가 달성되었다면 prove_goal로 하나씩 표시하세요. **최우선 기준: 목표가 미달성이고 open/running 의도가 전혀 없으면(frontier_open=0, running_intents 비어 있음) 이번 회차에 목표로 나아가는 의도를 하나 이상 만들어야 합니다. 대기 중인 작업도 실행 중인 작업도 없는데 0개를 만들면 작업이 멈춥니다. open/running 의도가 진행 중이거나 목표가 달성된 경우에만 새 의도를 만들지 않을 수 있습니다.**" +
		renderPlannerTodos(opts.Todos.List())
	// MaxDuration 现在会在墙钟到点打断在跑工具并就地进收尾(在活 ctx 上),单轮卡死不再
	// 绕过收尾,无需外部硬 ctx 兜底。ctx 只承载 pause / kill / shutdown。
	_, _, err = captureRun(ctx, opts, input,
		func(r db.Activity) {
			if emit != nil {
				r.Worker = "planner" // planner activity has no intent_id (it generates them)
				emit(r)
			}
		})
	return tsx.GoalMet, tsx.Reason, err
}
