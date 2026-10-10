package agent

import (
	"bytes"
	"runtime"
	"text/template"
	"time"
)

// PromptOverride, if set, returns the stored system-prompt template for an agent
// key and whether one exists. The server wires it to the PG agent_prompts table.
// When nil or no override exists, agents use their built-in default prompt — so
// behavior is identical until a user edits a prompt in the UI.
var PromptOverride func(agentKey string) (string, bool)

// Prompt-variable structs — fields mirror each agent's catalog (docs §5a) so a
// user template referencing a catalog variable renders; referencing anything else
// fails template execution and falls back to the built-in default.
type PlannerVars struct{ Goal, Scope, AssetSummary, DataDir, Now string }
type WorkerVars struct{ ProxyAddr, WorkerName, DataDir, Now string }
type MainVars struct{ Goal, AssetSummary, FindingsSummary, DataDir, Now string }
type GoalsVars struct{ EngagementDescription, DataDir, Now string }

// nowStr is the server-local wall-clock string exposed as the universal {{.Now}}
// prompt variable. renderSystem runs on every agent turn/round, so this is fresh
// each run — a prompt can subtract it from a fixed start stamp to reason about
// elapsed time (e.g. a timed benchmark's "last N hours" window).
func nowStr() string { return time.Now().Format("2006-01-02 15:04:05 MST") }

// renderSystem returns the rendered system-prompt BODY (段 [A]) for agentKey.
// Precedence: the DB-stored template (if any) over the built-in default template
// (def). BOTH are Go templates now — the built-in default is seeded into the DB
// verbatim, so the two paths render identically until a user edits the prompt.
// Rendering always runs (def used to be pre-substituted plain text; it is now a
// {{.Var}} template like the DB one). On any render error we fall back to the
// default template, then to the raw default string — an agent never starts with a
// half-rendered prompt. Callers append the code-owned tail (trafficTool / 中间产物
// 输出规约) AFTER this, so those can't be edited away via the DB body.
func renderSystem(agentKey, def string, vars any) string {
	tmpl := def
	if PromptOverride != nil {
		if t, ok := PromptOverride(agentKey); ok && t != "" {
			tmpl = t
		}
	}
	if out, err := renderTmpl(tmpl, vars); err == nil {
		return out
	}
	// DB template broke (e.g. references an out-of-catalog var) → code default.
	if out, err := renderTmpl(def, vars); err == nil {
		return out
	}
	return def
}

// langDirective is the artex-ko output-language tail: a code-owned segment
// appended AFTER the rendered body and the artifact/traffic tails on every
// user-facing agent role, so a DB-edited prompt body can never drop it — the same
// guarantee artifactSpec gives. It does NOT translate the agent "brain": the
// benchmarked Chinese reasoning body (段 [A]) stays verbatim. It only constrains
// the LANGUAGE of what the agent SHOWS to the user. Written in Chinese so it stays
// in the body's language (keeping the model's reasoning register stable) while
// forcing Korean OUTPUT — this is the localization approach: preserve behavior,
// localize the surface the user reads. Raw technical strings (commands, payloads,
// code, URLs, log/response excerpts) are explicitly kept verbatim so evidence and
// reproduction steps are not mangled by translation.
//
// Two anti-drift clauses were added after the end-to-end run (L1): live models
// leaked (1) Chinese into the planner's situation summary — mirroring the Chinese
// brain body (段 [A]) — and (2) English into report_finding's structured fields —
// mirroring an English target app/evidence. The directive now names the planner
// situation summary as a user-facing field and explicitly forbids mirroring BOTH
// the Chinese instruction language AND the target/material language in the display
// fields, so only the listed verbatim technical fragments stay non-Korean.
func langDirective() string {
	directive := "\n\n/no_think\n**한국어 우선 규칙**: 사용자에게 보이는 모든 자연어는 반드시 한국어로 작성하세요. record_fact의 summary/detail, report_finding의 제목·설명·결론·조치 권고, set_goals의 text, set_constraints의 text, add_task_scope의 reason, 플래너의 상황 요약, 최종 요약 및 사용자 대화가 모두 포함됩니다. 상세 보고서의 마크다운 제목도 '개요·영향·영향 범위·재현 절차·증거·원인·수정 권고'처럼 한국어로 쓰고 중국어 제목(概述·影响与危害·复现步骤·修复建议)을 사용하지 마세요. 명령, payload, 코드, 파일 경로, URL, 매개변수 이름과 로그·요청·응답의 원문만 번역하거나 고치지 말고 그대로 보존하세요. 위의 시스템 또는 역할 지시가 중국어이고 대상 자료가 영어·중국어여도 그 언어를 사용자 표시 문장에 따라 쓰지 마세요. 내부 분석 언어와 관계없이 사용자에게 보이는 본문은 첫 글자부터 한국어여야 합니다. 기술 원문을 제외한 영어·중국어 문장을 사용자에게 출력하지 마세요.\n重要：工具参数中所有面向用户的自然语言字段（set_goals.text、set_constraints.text、add_task_scope.reason、record_fact.summary/detail、report_finding.summary）以及规划摘要和最终答复，必须只用韩语书写。中文提示词只描述工作流程，不授权中文输出。提交工具调用前检查这些字段是否含中文；若有，先改写为韩语。"
	if runtime.GOOS == "windows" {
		directive += "\n\n**Windows 명령 실행 규칙**: 이 환경의 Bash 도구는 실제로 Windows PowerShell 5.1 명령을 실행합니다. `&&`와 `||` 연산자를 사용하지 말고 명령을 세미콜론(`;`)으로 구분하거나 PowerShell 제어문을 사용하세요. `curl` 별칭의 혼동을 피하려면 HTTP 요청에 `Invoke-WebRequest` 또는 `curl.exe`를 사용하세요. 같은 명령이 한 번 실패하면 인자만 바꿔 반복하지 말고 오류 원인을 읽어 명령 문법이나 접근 방식을 바꾸세요. 완전히 같은 실패 명령은 다시 호출하지 마세요."
	}
	return directive
}

func renderTmpl(tmpl string, vars any) (string, error) {
	t, err := template.New("p").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, vars); err != nil {
		return "", err
	}
	return b.String(), nil
}
