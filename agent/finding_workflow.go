package agent

import (
	"encoding/json"
	"fmt"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

const findingIDGuidance = "\n\n**취약점 번호 안내**: finding_id는 별도의 취약점 기록 ID이고 finding_node_id는 탐색 그래프 노드 ID입니다. list_findings / list_task_findings / node_detail / get_task_node_detail의 id는 탐색 노드 ID입니다. 독립 ID가 필요하면 같은 응답의 finding_id를 사용하세요. get_finding_traffic / bind_finding_traffic에는 finding_id를, update_finding_report에는 기존 호환 규칙에 따라 finding_node_id를 전달하세요. report_finding 첫 줄의 숫자를 증거 도구에 쓰지 말고, ID 오류가 나면 다른 숫자를 추측하지 마세요."

// The server supplies the persisted setting. A missing setting/host is off.
// Consulted at assembly and again on writes so an already-running session
// cannot keep binding after the user switches the feature off.
var FindingTrafficBindingEnabled func() bool

func findingTrafficBindingEnabled() bool {
	return FindingTrafficBindingEnabled != nil && FindingTrafficBindingEnabled()
}

// Applied after ToolResolve: user descriptions and prompts remain intact, while
// all actual reporters (including Planner and custom chat agents) see the same
// API contract. Disabled/unbound tools are never reintroduced here.
func findingWorkflowTools(agentKey string, tools []actool.CoreTool) ([]actool.CoreTool, string) {
	if !findingTrafficBindingEnabled() {
		out := make([]actool.CoreTool, 0, len(tools))
		for _, tool := range tools {
			if tool.Name() == "bind_finding_traffic" {
				continue
			}
			if agentKey == "reporter" && (tool.Name() == "traffic_search" || tool.Name() == "traffic_get" || tool.Name() == "traffic_blob") {
				continue
			}
			switch tool.Name() {
			case "report_finding", "add_hint", "add_task_hint":
				// Work on a copy: toggling back on must restore the original schema.
				raw, _ := json.Marshal(tool.InputSchema())
				var schema map[string]any
				if json.Unmarshal(raw, &schema) == nil {
					stripTrafficParameters(schema)
					tool = DecorateTool(tool, tool.Description(), schema)
				}
			}
			out = append(out, tool)
		}
		return out, ""
	}
	out := append([]actool.CoreTool(nil), tools...)
	has := map[string]bool{}
	for i, tool := range out {
		has[tool.Name()] = true
		note := ""
		switch tool.Name() {
		case "report_finding":
			note = "\n기본 흐름에서는 보고서 Agent가 보고서 작성 전에 트래픽을 확인하고 연결합니다. 보고자는 evidence에 검증 명령, 핵심 출력, 이미 확인한 실제 트래픽 ID와 용도를 남겨 실행 기록과 대조할 수 있게 하세요. 연결을 위해 추가로 트래픽을 수집할 필요는 없습니다. 즉시 연결하려면 검증된 참조만 traffic_refs 또는 evidence_hint_id로 전달하세요. 둘 중 하나라도 잘못되면 이번 보고 전체가 실패합니다. 연결 오류가 발생하면 다른 ID를 추측해 report_finding을 반복 호출하지 마세요. TCP 또는 트래픽 미수집 사례에서는 선택 항목을 생략하세요. 응답의 finding_id는 독립 기록 ID, finding_node_id는 탐색 노드 ID입니다. 정상 응답이나 명령 실행 성공은 취약점이 아니므로 report_finding을 호출하지 마세요."
		case "add_hint", "add_task_hint":
			note = "\n확인된 취약점을 인계할 때 대응하는 힌트의 traffic_refs에 검증된 트래픽 ID, 용도, 설명 및 순서를 보존하고(단일 항목은 최상위, 여러 항목은 해당 hints 요소에 입력), text에는 그 증거가 입증하는 구체적인 취약점을 적으세요. 기존 트래픽 참조가 있다면 텍스트만 인계하며 누락하지 마세요. 검증 전 후보는 증거로 전달하지 마세요."
		case "get_finding_traffic", "bind_finding_traffic", "list_findings", "list_task_findings", "node_detail", "get_task_node_detail", "update_finding_report":
			note = findingIDGuidance
		}
		if note != "" {
			out[i] = DecorateTool(tool, tool.Description()+note, tool.InputSchema())
		}
	}
	guidance := ""
	if has["report_finding"] || has["add_task_hint"] || has["add_hint"] {
		guidance = "\n\n**트래픽 증거 인계(선택 사항)**: 기본적으로 취약점 등록 후 보고서 Agent가 작성 전에 트래픽을 확인하고 연결합니다. 보고자는 evidence에 검증 명령, 핵심 출력, 확인된 실제 트래픽 ID와 용도를 남기고 intent_id를 포함해 추적 가능하게 하세요. 연결을 위해 추가 수집하지 마세요. 명시적으로 즉시 연결할 때만 검증된 traffic_refs 또는 evidence_hint_id를 사용하세요. Auto / Planner가 대신 보고할 때 실행자가 남긴 참조를 누락하지 마세요. add_hint / add_task_hint는 traffic_refs로 인계할 수 있습니다. TCP 또는 트래픽이 없는 경우 정상 등록할 수 있으며, ID를 추측하거나 증거를 만들려고 재탐색하지 마세요. 정상 HTTP 응답이나 도구 실행 성공은 취약점이 아닙니다. report_finding에 잘못된 traffic_refs를 넣어 실패하면 다른 ID로 재시도하지 말고 사실만 record_fact로 남기세요."
		if has["add_task_hint"] && !has["add_hint"] {
			guidance += "\n플랫폼 대화에는 작업 문맥이 없으므로 report_finding을 직접 호출하지 마세요. add_task_hint로 해당 작업에 인계하고 작업 Agent가 등록하도록 한 뒤 list_task_findings로 결과를 확인하세요."
		}
		if has["prove_goal"] || has["goal_met"] {
			guidance += "\n목표 달성을 판단하기 전에 이번에 이미 확보된 증거의 등록/인계를 끝내세요. 텍스트로 취약점을 기록했다는 이유만으로 증거 인계 전에 작업을 끝내거나 Worker를 취소하지 마세요. 트래픽이 없다고 기다리거나 강제로 수집할 필요는 없습니다."
		}
	}
	if has["update_finding_report"] && has["bind_finding_traffic"] && has["get_finding_traffic"] {
		guidance += "\n\n**보고서 작성 전 트래픽 자동 연결(활성화됨)**: 이번에 호출된 취약점을 대상으로 트래픽을 확인하고 연결한 뒤 보고서를 작성하세요. 먼저 report_finding 응답 JSON 또는 get_task_node_detail / list_task_findings에서 명확한 finding_id와 finding_node_id를 확인하세요. 취약점 상세, 해당 의도의 실행 기록, 기존 증거 목록을 읽고 보고자가 인계한 실제 ID를 우선 대조하세요. 이번 검증이 HTTP이고 트래픽 도구를 사용할 수 있으면 traffic_search로 후보를 찾고 traffic_get으로 요청/응답이 실제 취약점을 뒷받침하는지 각각 확인하세요. 도메인과 시각은 후보 검색에만 사용하며 소유 관계를 증명하지 않습니다. 확인한 증거만 재현 순서대로 bind_finding_traffic(finding_id, traffic_refs)에 연결하고 용도를 설명하세요. 이번 취약점만 처리하고 중복 등록하거나 재탐색하지 마세요. 연결 후 get_finding_traffic을 다시 호출해 최신 version을 확인하고, 필요한 본문을 읽은 다음 실제로 읽은 version을 evidence_version으로 update_finding_report에 전달하세요(이때 finding_id에는 finding_node_id를 사용합니다). 이미 연결된 증거는 중복 추가하지 마세요. TCP, 미수집, 도구 사용 불가, 확실한 일치 항목 없음은 자동 연결을 건너뛰고 보유한 텍스트/명령 증거로 보고서를 작성하세요. ID를 추측하거나 연결 실패를 성공이라고 보고하지 마세요. 실패가 반복되면 중단하고 오류를 한국어로 요약하세요."
	}
	if guidance != "" || has["get_finding_traffic"] || has["update_finding_report"] {
		guidance += findingIDGuidance
	}
	return out, guidance
}

func stripTrafficParameters(schema map[string]any) {
	props, _ := schema["properties"].(map[string]any)
	delete(props, "traffic_refs")
	delete(props, "evidence_hint_id")
	if required, ok := schema["required"].([]any); ok {
		kept := required[:0]
		for _, key := range required {
			if key != "traffic_refs" && key != "evidence_hint_id" {
				kept = append(kept, key)
			}
		}
		schema["required"] = kept
	}
	if hints, ok := props["hints"].(map[string]any); ok {
		if items, ok := hints["items"].(map[string]any); ok {
			stripTrafficParameters(items)
		}
	}
}

// HintTrafficSchema is shared by the task-local and cross-task hint tools.
func HintTrafficSchema() map[string]any {
	return map[string]any{"type": "array", "description": "可选：已核实且对应本提示中具体漏洞的流量引用，保留顺序；交接后 report_finding 可传 evidence_hint_id 携带这些引用。", "items": obj(map[string]any{"traffic_id": str("真实流量 ID"), "role": str("baseline / proof / verification / supporting"), "note": str("该流量支持什么结论")}, "traffic_id")}
}

func (t *ToolSet) findingRefsFromHint(hintID int64, explicit []db.TrafficRef) ([]db.TrafficRef, error) {
	if hintID <= 0 {
		return db.NormalizeTrafficRefs(explicit)
	}
	n, err := t.ts.GetNode(hintID) // local store only: inherited hints cannot supply evidence
	if err != nil {
		return nil, err
	}
	if n == nil || n.Kind != db.KindHint {
		return nil, fmt.Errorf("evidence_hint_id=%d 必须是本任务的提示节点（继承提示不可直接用于绑定）", hintID)
	}
	var payload struct {
		Refs []db.TrafficRef `json:"traffic_refs"`
	}
	if err := json.Unmarshal(n.Payload, &payload); err != nil {
		return nil, err
	}
	return db.NormalizeTrafficRefs(append(append([]db.TrafficRef{}, explicit...), payload.Refs...))
}
