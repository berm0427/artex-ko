package agent

import (
	"context"

	"github.com/Autumn-27/artex/db"
)

// FindingRecorder is injected by the host; agents never synthesize or copy
// evidence bodies themselves. Its implementation owns the atomic write.
type FindingRecorder interface {
	Record(context.Context, db.RecordFindingInput, []db.TrafficRef) (*db.RecordedFinding, error)
}

// Tool-use guidance is appended without replacing the user's editable prompt.
// It does not require capture or claim that unavailable traffic tools exist.
const findingTrafficGuidance = "\n\n**취약점 트래픽 증거(선택)**: report_finding으로 취약점을 등록할 때 직접 확인했고 결론을 뒷받침하는 HTTP 요청·응답이 있다면 traffic_refs에 실제 ID를 재현 순서대로 연결하세요. 도메인과 시간은 후보 필터일 뿐 연결 근거가 아닙니다. TCP 등 HTTP가 아닌 취약점이나 캡처가 없거나 정확히 일치하는 항목이 없다면 생략하거나 []를 전달하세요. evidence에는 명령 출력·로그 등 다른 검증 가능한 증거를 보존하고 연결하지 않은 이유를 설명하는 것이 좋습니다. ID를 추측하거나 트래픽 보충만을 위해 대상을 반복 탐색하지 마세요."

func (t *ToolSet) SetFindingRecorder(r FindingRecorder)   { t.findingRecorder = r }
func (w *Worker) SetFindingRecorder(r FindingRecorder)    { w.findingRecorder = r }
func (p *Planner) SetFindingRecorder(r FindingRecorder)   { p.findingRecorder = r }
func (m *MainAgent) SetFindingRecorder(r FindingRecorder) { m.findingRecorder = r }
