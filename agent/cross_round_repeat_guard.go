package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	actool "github.com/Autumn-27/norma/tool"

	"github.com/Autumn-27/artex/db"
)

// cross_round_repeat_guard is a HARD circuit-breaker for the cross-round planner
// loop. The per-round repeat guards (e.g. list_intents' `seen` set in tools.go)
// reset every round because Plan() builds a fresh ToolSet per round, and the only
// cross-round signal — recentProgressAudit — is advisory text a small local model
// ignores. Worse, that audit excludes NodeID==nil rows, which is exactly where the
// planner's own list_intents/graph_overview land, so it never even names the loop.
//
// This guard reads the activity log directly, detects a planner read-call repeated
// across rounds with no new evidence since, and (gated to local models in Plan)
// forces a wind-down round: add_intent is dropped and the planner is told to judge
// the goal and report proven limits instead of re-issuing the same read.

// crossRoundRepeatThreshold is how many identical planner calls, back-to-back
// across rounds with no new evidence since the streak began, trip the breaker.
const crossRoundRepeatThreshold = 3

// crossRoundEvidenceTools are tool calls that record genuine new state. If one of
// them lands after a repeated-read streak began, the repeats are NOT a stall — the
// planner is reacting to fresh evidence, so the breaker must stay quiet.
var crossRoundEvidenceTools = map[string]bool{
	"add_intent":     true,
	"record_fact":    true,
	"report_finding": true,
	"insert_assets":  true,
}

// crossRoundInventoryTools are the planner's read-only inventory polls. Alternating
// between them (each with default/first-page args) returns nothing new yet evades the
// per-summary streak because no single summary repeats enough. They share one
// synthetic bucket (crossRoundBlindPollBucket) so an alternating empty-poll loop still
// trips the breaker — but only their blind (non-paginating) calls, so genuine
// pagination through pages stays out of the bucket.
var crossRoundInventoryTools = map[string]bool{
	"list_facts":     true,
	"list_intents":   true,
	"list_findings":  true,
	"graph_overview": true,
}

// crossRoundCursorKeys are the numeric pagination cursors. A call carrying one with a
// value > 0 is advancing through pages (real progress), not a blind re-poll.
var crossRoundCursorKeys = []string{"before", "offset", "after"}

// crossRoundBlindPollBucket keys the synthetic blind-inventory-poll group. It is the
// empty string, which can never collide with a real call: those are skipped when
// a.Summary == "".
const crossRoundBlindPollBucket = ""

// crossRoundCall pairs an activity's ID with the tool that produced it, so the
// synthetic bucket (which mixes tools) can still report the most recent tool name.
type crossRoundCall struct {
	id   int64
	tool string
}

// isCrossRoundBlindPoll reports whether a summary's JSON args carry no advancing
// numeric cursor (> 0). A call with no args, or whose args don't parse, is treated as
// a blind poll (same as an argument-less call).
func isCrossRoundBlindPoll(summary string) bool {
	_, rawArgs, _ := strings.Cut(summary, " ")
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return true
	}
	for _, key := range crossRoundCursorKeys {
		if cursor, ok := args[key].(float64); ok && cursor > 0 {
			return false
		}
	}
	return true
}

// crossRoundRepeatStall loads the recent activity log and reports whether the
// planner is stuck re-issuing the same call with nothing new to show for it. The
// returned string names the repeated tool (for the directive). Derived purely from
// the log, so it adds no state and is safe to call once per planner round.
func crossRoundRepeatStall(ts *db.ExplorationStore) (string, bool) {
	if ts == nil {
		return "", false
	}
	activities, _, err := ts.ActivityPage(db.ActivitySessionFilter{}, 0, 600)
	if err != nil {
		return "", false
	}
	return detectCrossRoundStall(activities)
}

// detectCrossRoundStall is the pure core: group the planner's own tool calls by
// their exact summary (which includes the arguments), plus a synthetic bucket that
// pools blind inventory polls across tools, and flag any group whose most recent
// crossRoundRepeatThreshold calls all occurred after the last SUCCESSFUL
// evidence-recording call anywhere in the task. Two things keep it honest:
//   - Only a non-errored evidence call (add_intent/record_fact/... with IsError==false)
//     counts as progress. A rejected duplicate add_intent is NOT progress, so it does
//     not reset the streak; instead it falls through and is counted as a repeated
//     non-productive planner call in its own summary group.
//   - Evidence from a worker counts, so a planner re-reading while a worker makes
//     progress is not flagged.
func detectCrossRoundStall(activities []db.Activity) (string, bool) {
	var lastEvidenceID int64
	groups := map[string][]crossRoundCall{}
	for _, a := range activities {
		if a.Kind != "tool_use" || a.Tool == "" {
			continue
		}
		// Only a SUCCESSFUL evidence call counts as progress; a rejected one falls
		// through to be grouped as a repeated non-productive planner call.
		if crossRoundEvidenceTools[a.Tool] && !a.IsError {
			if a.ID > lastEvidenceID {
				lastEvidenceID = a.ID
			}
			continue
		}
		// Only the planner's own loop is in scope for the planner breaker; worker
		// repeats keep their existing (advisory) progress-audit path.
		if a.Worker != "planner" || a.Summary == "" {
			continue
		}
		call := crossRoundCall{id: a.ID, tool: a.Tool}
		groups[a.Summary] = append(groups[a.Summary], call)
		// Blind inventory polls also pool into one bucket so alternating empty reads
		// across different inventory tools still accumulate toward the threshold.
		if crossRoundInventoryTools[a.Tool] && isCrossRoundBlindPoll(a.Summary) {
			groups[crossRoundBlindPollBucket] = append(groups[crossRoundBlindPollBucket], call)
		}
	}
	bestTool := ""
	var bestRecent int64 = -1
	for _, calls := range groups {
		if len(calls) < crossRoundRepeatThreshold {
			continue
		}
		sort.Slice(calls, func(i, j int) bool { return calls[i].id < calls[j].id })
		streakStart := calls[len(calls)-crossRoundRepeatThreshold].id
		if streakStart <= lastEvidenceID {
			continue // new evidence landed within the streak — real progress
		}
		if recent := calls[len(calls)-1]; recent.id > bestRecent {
			bestRecent, bestTool = recent.id, recent.tool
		}
	}
	if bestTool == "" {
		return "", false
	}
	return bestTool, true
}

// crossRoundStallDirective is the hard wind-down instruction injected into the
// stalled round. It forbids another duplicate read/intent and routes the planner
// to a goal judgment or an honest limits report.
func crossRoundStallDirective(tool string) string {
	return fmt.Sprintf("동일한 planner 조회(%s)가 여러 계획 회차에 걸쳐 반복됐고, 그 사이 새 사실·취약점·의도가 하나도 추가되지 않았습니다. 이번 회차에는 add_intent를 호출하지 마세요(이번 회차 도구 목록에서 제외됨). 같은 조회를 다시 반복하지 말고, 이미 성공한 도구 출력으로 확인된 내용만 근거로 목표 달성 여부를 판정하세요. 더 검증할 새 가설(바꿀 변수 하나와 기대하는 관찰 차이)이 없다면 입증된 한계를 기록하고 종료하세요.", tool)
}

// dropToolByName returns tools without the named tool (no-op if absent). Used to
// physically remove add_intent from a stalled wind-down round so the model cannot
// emit yet another duplicate intent.
func dropToolByName(tools []actool.CoreTool, name string) []actool.CoreTool {
	out := tools[:0:0]
	for _, t := range tools {
		if t.Name() == name {
			continue
		}
		out = append(out, t)
	}
	return out
}
