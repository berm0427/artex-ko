package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Worker is an LLM work agent (docs §4.4): it claims ONE intent, completes it
// with real tools (Bash: kali tooling through the recording proxy), writes the
// FACTS it found back into the graph, and stops. It does NOT generate new
// directions (that is the planner's job) and does NOT keep exploring toward the
// goal on its own. Multiple workers run concurrently as goroutines.
// WebSearchOpts is the web-search backend selection the server pushes into each
// agent (planner/worker/main). Enabled=false leaves the web_search tool off.
// Backend is "ddgs" (no key), "brave-free" (BraveKey required), "tavily"
// (TavilyKey required), or "deepseek" (DeepSeek* required, filled from the
// active LLM profile). It maps directly onto agentcore.Options.
// Proxy is a dedicated egress proxy for the search request (http/https/socks5),
// independent of the traffic-recording MITM proxy — set it when the search endpoint
// is only reachable via a VPN/SOCKS proxy. Empty = direct.
//
// 注意 deepseek 后端与其它三个的性质不同：DeepSeek 没有可直接调用的搜索接口，
// 搜索只存在于其 Anthropic 兼容 messages 接口内部(web_search_20250305 server
// tool)，因此每次搜索会消耗一次模型调用，且搜索请求由 DeepSeek 服务端发出——
// 不经过本机 Proxy，也不会进流量留痕。
type WebSearchOpts struct {
	Enabled   bool
	Backend   string
	BraveKey  string
	TavilyKey string
	Proxy     string
	// DeepSeek* 来自当前激活的 LLM 配置(仅 anthropic 格式的 DeepSeek 官方端点)，
	// 不单独配置，随 LLM 配置切换而变。
	DeepSeekBaseURL string
	DeepSeekAPIKey  string
	DeepSeekModel   string
}

type Worker struct {
	findingRecorder FindingRecorder
	prov            llm.Provider
	model           string
	workDir         string
	proxyAddr       string
	proxyCACert     string            // recording proxy's CA cert path (for WebFetch HTTPS verify)
	webSearch       WebSearchOpts     // web_search tool backend selection (off by default)
	tx              *transcript.Store // raw LLM conversation persistence (nil = off)
	window          int               // context window in tokens (for compaction)
	windowFn        func() int        // optional dynamic task-chain minimum
	maxTurns        int               // max agent turns per run (0 = unlimited)
	// runTimeout is the wall-clock budget for the main exploration of one intent
	// (0 = unlimited). When it fires, the run is cut and a settlement round is
	// forced so already-identified facts get written back instead of being lost.
	runTimeout time.Duration
	// extraTools are host-provided tools (e.g. traffic query, oast) appended to
	// the worker's graph write-back tools.
	extraTools []actool.CoreTool
	// injectConstraints resolves whether this task's operation constraints get
	// injected into the worker system prompt. Read per run so the settings toggle
	// takes effect without rebuilding the agent. nil = inject (default).
	injectConstraints func() bool
	// nonStreamingFn resolves whether this run uses the non-streaming (Complete)
	// path. Read per run so a profile/task toggle takes effect without rebuilding
	// the agent. nil = streaming (default).
	nonStreamingFn func() bool
	// noaEnabledFn resolves whether this run uses the experimental noa context-
	// compression mechanism. Read per run, like nonStreaming. nil = off (built-in
	// compaction).
	noaEnabledFn func() bool
	// maxTokensFn resolves the per-reply output cap in tokens, on the same
	// per-run basis. nil or 0 = send no cap and let the endpoint decide.
	maxTokensFn func() int
	noThinkFn   func() bool
}

// WorkerSessionID returns the stable transcript key used by a worker intent.
// Worker slots are reusable, so the intent id (rather than work#N) is the
// session identity. Keep this helper public so the Worker message API and UI
// can refer to exactly the conversation that will be resumed.
func WorkerSessionID(explorationID, intentID int64) string {
	return fmt.Sprintf("exp%d-worker-i%d", explorationID, intentID)
}

const workerChatMarkerPrefix = "<!-- ARTEX_WORKER_CHAT:"

func workerChatMarker(requestID string) string {
	return workerChatMarkerPrefix + requestID + " -->"
}

func hasWorkerChatMessage(messages []llm.Message, requestID string) bool {
	marker := workerChatMarker(requestID)
	for _, message := range messages {
		if message.Role == llm.RoleUser && strings.Contains(message.Text(), marker) {
			return true
		}
	}
	return false
}

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default). Read per
// run so a profile or task-chain toggle takes effect without rebuilding.
func (w *Worker) SetNonStreaming(fn func() bool) { w.nonStreamingFn = fn }

func (w *Worker) nonStreaming() bool { return w.nonStreamingFn != nil && w.nonStreamingFn() }

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (w *Worker) SetNoaEnabled(fn func() bool) { w.noaEnabledFn = fn }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (w *Worker) SetMaxTokens(fn func() int) { w.maxTokensFn = fn }

// SetNoThink resolves a model-family-specific Qwen3 prompt switch per run.
func (w *Worker) SetNoThink(fn func() bool) { w.noThinkFn = fn }

func (w *Worker) maxTokens() int {
	if w.maxTokensFn == nil {
		return 0
	}
	return w.maxTokensFn()
}

// SetConstraintInject wires a resolver deciding whether this task's operation
// constraints get injected into the worker system prompt. nil = inject (default).
func (w *Worker) SetConstraintInject(fn func() bool) { w.injectConstraints = fn }

// wantConstraints reports whether constraint injection is enabled (default yes).
func (w *Worker) wantConstraints() bool { return w.injectConstraints == nil || w.injectConstraints() }

// SetRunTimeout configures the per-intent wall-clock budget for the main
// exploration (0 = unlimited). When it fires, the SDK settlement phase still runs
// so facts are never lost to a timeout. Safe to call before Execute.
func (w *Worker) SetRunTimeout(run time.Duration) {
	w.runTimeout = run
}

// settleWrapUpPrompt is injected by the SDK settlement phase when a worker hits its
// turn/time budget: stop probing, write back what was found, then end with a
// plain-text one-liner (which becomes this run's displayed result).
const settleWrapUpPrompt = "이번 실행이 예산 소진으로 곧 종료됩니다. 더 이상 명령이나 탐지를 실행하지 마십시오. 성공한 도구 응답으로 직접 확인한 내용 중 아직 저장하지 않은 것만 insert_assets, record_fact, report_finding으로 기록하세요. 실패·취소된 명령의 출력은 증거로 만들거나 추측하지 마십시오. 저장 도구가 실패하면 같은 내용을 재시도하지 말고 실패 사실을 최종 요약에 남기세요. 저장할 검증된 내용이 없으면 저장 도구를 호출하지 마십시오. 마지막에 한 문장의 한국어 순수 텍스트로 실제 확인된 결과와 미완료 사유를 요약하세요."

func NewWorker(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int, extra ...actool.CoreTool) *Worker {
	return &Worker{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns, extraTools: extra}
}

// defaultToolsExcept returns actool.DefaultTools() minus the named tools (by
// CoreTool.Name()). Used to trim SDK default tools an agent shouldn't have.
func defaultToolsExcept(exclude ...string) []actool.CoreTool {
	drop := make(map[string]bool, len(exclude))
	for _, n := range exclude {
		drop[n] = true
	}
	all := actool.DefaultTools()
	out := make([]actool.CoreTool, 0, len(all))
	for _, t := range all {
		if !drop[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

func (w *Worker) SetCompactionWindowResolver(fn func() int) { w.windowFn = fn }

func (w *Worker) compactionWindow() int {
	if w.windowFn != nil {
		return w.windowFn()
	}
	return w.window
}

// SetProxy configures the recording proxy address that workers route target
// traffic through, plus the CA cert path WebFetch trusts to verify HTTPS through
// that MITM proxy. Empty addr disables the hint.
func (w *Worker) SetProxy(addr, caCert string) { w.proxyAddr, w.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for this worker (off by default).
func (w *Worker) SetWebSearch(o WebSearchOpts) { w.webSearch = o }

// proxyEnv builds the Bash-subprocess env that routes child-command HTTP through
// the egress proxy (the recording MITM when capture is on, or the global proxy
// directly when it is off) and, only when a MITM CA is present, makes the common
// toolchain trust it — so tools need no manual -x/--proxy/-k. Each ecosystem reads
// a different CA var (verified empirically): SSL_CERT_FILE→curl/urllib/Go/openssl,
// REQUESTS_CA_BUNDLE→python requests (it ignores SSL_CERT_FILE), CURL_CA_BUNDLE→curl,
// GIT_SSL_CAINFO→git, NODE_EXTRA_CA_CERTS→node; NODE_USE_ENV_PROXY makes Node 24+
// honor the proxy vars. ALL_PROXY is set too so a socks5 egress proxy (which curl
// only reads from ALL_PROXY, not HTTP(S)_PROXY) works in the capture-off path.
// Empty proxyAddr → nil (direct, unchanged env).
func proxyEnv(proxyAddr, caCert string) []string {
	if proxyAddr == "" {
		return nil
	}
	env := []string{
		"HTTP_PROXY=" + proxyAddr, "HTTPS_PROXY=" + proxyAddr,
		"http_proxy=" + proxyAddr, "https_proxy=" + proxyAddr,
		"ALL_PROXY=" + proxyAddr, "all_proxy=" + proxyAddr, // socks5 egress: curl reads only this
		"NODE_USE_ENV_PROXY=1", // Node 24+: honor HTTP(S)_PROXY in built-in fetch/http
	}
	if caCert != "" {
		env = append(env,
			"SSL_CERT_FILE="+caCert,
			"CURL_CA_BUNDLE="+caCert,
			"REQUESTS_CA_BUNDLE="+caCert,
			"GIT_SSL_CAINFO="+caCert,
			"NODE_EXTRA_CA_CERTS="+caCert,
		)
	}
	return env
}

// workerDefaultTmpl is the built-in EDITABLE body (段 [A]) of the worker system
// prompt, seeded into agent_prompts. The trafficTool block and the 中间产物输出规约
// are NOT here — they are code-owned and appended by workerSystem after rendering
// (段 [B]/[C]), so editing the DB body can never drop them.
const workerDefaultTmpl = `당신은 허가된 보안 검증 작업자입니다. 지정된 의도와 허가된 범위만 수행하세요. 필요한 도구를 호출해 실제 응답을 확인한 뒤 결론을 쓰세요. 각 객체가 소유자에게만 공개되는 정책에서 사용자 A의 인증 요청으로 사용자 B 소유 비공개 객체가 HTTP 200으로 반환되고 내용이 드러나면 IDOR 취약점입니다. 이 경우 record_fact가 아니라 report_finding을 호출하고, 현재 intent_id와 성공한 도구 출력에 그대로 존재하는 16자 이상의 evidence_quote를 별도 JSON 필드로 전달하세요. 접근이 거부되거나 정책이 불분명하면 취약점으로 보고하지 마세요. 동일한 결과를 반복 기록하지 말고 한 번 기록한 뒤 종료하세요.`

// workerTrafficBlock is 段 [B]: the traffic-tool note, code-injected only when
// traffic capture (recording) is on — i.e. the traffic_* tools actually exist.
// Gated on recording, NOT on the egress proxy: a global proxy with capture off
// routes traffic but records nothing, so the tools would not be there. Not stored,
// not editable.
func workerTrafficBlock(recording bool) string {
	if !recording {
		return ""
	}
	return "\n\n**트래픽 도구**:\n- traffic_search / traffic_get / traffic_blob: 이미 받은 응답과 방문한 자원을 확인하세요. 같은 URL을 curl로 다시 요청하기 전에 기록된 트래픽을 검색하세요. traffic_search에는 host가 필수이며 기본 반환값은 응답 본문이 없는 경량 인덱스 3개(id/method/url/status/resp_len)입니다. 더 필요하면 limit을 명시하세요. body_contains로 요청·응답 본문을 검색할 수 있습니다(최소 3자, 부분 문자열 및 한국어 지원). 원문은 traffic_get(id)으로 확인하고, 큰 본문이 @blob sha256:<hash>로 표시되면 traffic_blob(hash)으로 나누어 읽으세요."
}

// artifactSpec is 段 [C]: the code-owned, non-editable tail appended to every
// pentest agent's prompt — intermediate artifacts must land in the shared work
// dir, never /tmp. Guaranteed present regardless of how the DB body is edited.
func artifactSpec(dir string) string {
	return "\n\n**중간 산출물 경로**: 스크립트, 페이로드, 수집한 응답 본문, 임시 데이터는 모두 이 작업 디렉터리에 저장하세요: " + dir + ". 상대 경로 또는 표시된 절대 경로를 사용하고, /tmp나 다른 절대 경로에는 쓰지 마세요."
}

// workerArtifactSpec is the worker's 段 [C]: its per-intent run dir is pre-created
// by the engine (ensureRunDir), so it just writes relative paths there — no manual
// mkdir, no cross-worker name collisions.
func workerArtifactSpec(runDir string) string {
	return "\n\n**중간 산출물 경로**: 스크립트, 페이로드, 수집한 응답 본문, 임시 데이터는 모두 이 의도의 전용 작업 디렉터리에 저장하세요: " + runDir + ". 디렉터리는 이미 생성됐으므로 상대 경로를 바로 사용할 수 있습니다. /tmp나 다른 절대 경로에는 쓰지 마세요."
}

const remoteNetworkEvidenceRule = "\n\n**원격 연결 진단**: 로컬 netstat의 LISTEN 목록은 원격 서버 상태를 보여주지 않습니다. 원격 포트 연결 실패는 관찰한 오류와 시각만 기록하고, 서버 프로세스 중단·방화벽·VM 만료 중 어느 원인인지 확인 없이 단정하지 마세요. 단일 허가 포트에서 소수의 재시도 후에도 연결할 수 없다면 다른 포트를 탐색하거나 같은 시도를 반복하지 말고 접근 불가로 보고하세요."

// A code-owned rule survives editable DB prompt overrides for both roles.
const serviceProtocolEvidenceRule = "\n\n**서비스 프로토콜 판별(필수)**: 대상 포트가 HTTP 웹 서비스라는 결론은 실제 HTTP 상태줄과 헤더 또는 동등하게 검증된 HTTP 교환이 있을 때만 내리세요. 포트 번호, URL 표기, curl의 HTTP/0.9 오류, 배너·주소 문자열만으로 HTTP라고 추정하지 마세요. 상태줄 없이 원시 텍스트(예: `Address of the buf: 0x...`)가 먼저 오면 raw TCP/미확인 프로토콜로 취급하고, HTTP 전용 SQLi·XSS·디렉터리·파라미터·세션 탐색 의도를 생성하거나 실행하지 마세요. 기존 사실이나 의도가 HTTP라고 주장해도 원문 증거와 대조해 틀리면 그 가정을 폐기하세요. 먼저 허가된 단일 포트의 실제 입출력으로 프로토콜을 확인하고, 확인된 프로토콜에 맞는 후속 작업만 수행하세요. 확인 불가라면 웹 취약점이 아니라 프로토콜 미확인으로 보고하세요."

// Keep the choice of investigation method evidence-led, not tied to a challenge
// label, port, goal wording, or the examples in an editable prompt body.
const evidenceLedMethodRule = "\n\n**증거 기반 방법 선택(필수)**: 작업 제목·포트 번호·플래그 형식·이전 예시만으로 웹/포너블/리버싱/포렌식 등 유형을 확정하지 마세요. 먼저 실제로 제공된 표면(HTTP 교환, 원시 소켓 입출력, 파일·바이너리·로그 등)을 관찰하고, 잠정 가설·근거·불확실성을 구분한 뒤 그 표면을 검사할 수 있는 도구를 스스로 선택하세요. 관찰과 맞지 않으면 분류와 도구를 바꾸세요. 원시 TCP 프롬프트는 셸 권한이나 웹 API의 증거가 아니며, 플래그 접두어·셸 명령·웹 취약점 이름을 응답 근거 없이 입력하거나 결론에 끼워 넣지 마세요. 연결 확인이나 무응답 몇 건만으로 분석 완료·취약점 없음이라고 하지 말고, 실제 입출력에서 다음에 구별할 수 있는 검증 질문을 제시하세요. 특정 유형 하나만 강제하지 않으며, 자료가 없는 유형은 추측 대신 미확인으로 남기세요."

const serviceProgressRule = "\n\n**반복 탐색 방지(필수)**: 같은 대상·포트에 대해 이미 완료된 의도와 사실을 먼저 확인하세요. 원문 응답으로 HTTP가 아니며 원시 TCP라고 확인했다면 SSH/Telnet/Redis/MQTT 등의 일반적인 배너 이름을 바꿔 나열하며 '서비스 유형 식별' 의도를 다시 만들지 마세요. 그 결과는 이미 확보한 진행 상황입니다. 다음 의도는 앞선 의도와 다른 검증 질문, 사용할 입력, 관찰할 출력 차이를 명시해야 합니다. 새 관측값이나 새로운 검증 방법이 없으면 동일한 탐색을 재시도하지 말고 중복이라고 보고하세요. 워커도 이미 실패한 명령을 그대로 또는 겉모양만 바꿔 재실행하지 마세요. 원시 TCP 입출력에는 셸 명령을 조립하기 전에 tcp_exchange 도구를 사용하세요. Windows에서는 Bash 도구가 PowerShell 5.1이므로 `timeout`, `||`, `&&` 같은 Unix 셸 문법을 쓰지 마세요. curl.exe의 `tcp://` URL은 원시 TCP 접속 방법이 아닙니다."

// ensureRunDir builds and creates an agent's working directory under base:
// <base>/tasks/<taskID> for planner/main; <base>/tasks/<taskID>/i<intentID> for a
// worker (intentID<=0 → task dir only). The "tasks/" segment groups per-task dirs
// symmetrically with the chat agent's "sessions/<sessionID>". Best-effort mkdir — on
// failure, writes fail the same way an unwritable CWD would.
func ensureRunDir(base string, taskID, intentID int64) string {
	dir := filepath.Join(base, "tasks", strconv.FormatInt(taskID, 10))
	if intentID > 0 {
		dir = filepath.Join(dir, "i"+strconv.FormatInt(intentID, 10))
	}
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// cmdOutDir is the SDK large-tool-output spill dir under an agent's run dir.
func cmdOutDir(dir string) string { return filepath.Join(dir, "cmd-output") }

const blindProtocolExplorationRule = "\n\n**미확인 입력 프로토콜 탐색(필수)**: `Input:`처럼 입력 프롬프트만 보이면 임의의 명령어·플래그 문자열을 나열하지 마세요. 각 연결의 실제 전송·수신 바이트를 기록하고, 줄바꿈 유무·종결 문자·짧은 길이와 단계적으로 늘린 길이 등 서로 한 조건만 다른 입력을 비교해 입력 경계와 후속 응답을 파악하세요. 필요하면 동일 연결에서 여러 입력을 처리할 수 있는 원시 TCP 도구를 사용하세요. 프롬프트만 되풀이되거나 몇 차례 무응답이었다는 사실은 '취약점 없음'의 증거가 아닙니다. 관찰한 차이와 아직 확인하지 못한 부분을 분리해 기록하고, 판별 가능한 후속 실험이 남았다면 완료로 단정하지 마세요."

func workerSystem(proxyAddr, caCert, dataDir, runDir string) string {
	body := renderSystem("worker", workerDefaultTmpl, WorkerVars{ProxyAddr: proxyAddr, DataDir: dataDir, Now: nowStr()})
	body += blindProtocolExplorationRule
	// caCert is present only when the recording MITM is on, which is exactly when
	// the traffic_* tools are registered — so it gates the traffic-tool note.
	// Optional finding guidance is added for every role after tool resolution.
	return body + workerTrafficBlock(caCert != "") + workerArtifactSpec(runDir) + remoteNetworkEvidenceRule + evidenceLedMethodRule + serviceProtocolEvidenceRule + serviceProgressRule + "\n\n같은 연결에서 프롬프트에 맞춰 여러 차례 응답해야 하는 원시 TCP 서비스는 tcp_session을 사용하세요. 모델이 도구 호출을 준비하는 동안 서버 입력 제한 시간이 끝날 수 있으므로, 첫 입력을 이미 정했다면 open과 send를 나누지 말고 open_send 한 번으로 배너 수신과 입력 전송을 연속 수행하세요. 사전에 정한 여러 입력을 짧은 제한 시간 안에 연속 전송하려면 별도 도구 tcp_sequence(host, port, steps)를 사용하세요. 한 바이트의 반복 입력만 필요하면 인자가 단순한 tcp_repeat_sequence(host, port, steps:[{byte_hex,count,append_newline}])를 사용하세요. 각 step의 parts에 repeat_hex와 count를 지정하면 긴 바이너리 입력을 정확히 만들 수 있습니다. 사전에 정할 수 없는 다음 입력은 한 번의 로컬 프로그램에서 응답을 파싱해 처리해야 하며, 느린 모델 호출 사이에 연결이 살아 있다고 가정하지 마세요. 입력값을 모른다는 이유로 도구 호출 없이 종료하지 마세요. 현재 의도와 기존 사실에서 입력을 복원하고, 부족하면 node_detail 또는 get_worker_trace로 근거를 읽은 뒤 허가된 대상에 한정해 최소 진단 호출을 하세요. 진단 결과가 없어야만 근거 부족으로 종료하세요. 주소·$rbp 거리·카나리 안내는 바이너리 입출력 단서이며 셸 명령 프롬프트로 간주하지 마세요. `[1]` 같은 번호 표기만으로 메뉴라고 단정하지 말고, 시험 입력 뒤 실제 상태 변화를 확인하세요. `Input:`처럼 입력을 기다리는 프롬프트만 받은 것은 입력 처리 결과가 아닙니다. 문자열 입력에는 send_text와 append_newline을 쓰고, 바이너리·반복 바이트는 parts로 조립할 수 있습니다. 응답이 0바이트면 idle_timeout과 peer_closed를 확인하고 read_wait_ms를 조정해 한 번 더 읽으세요. 실제 다음 단계 응답 없이 의도를 완료했다고 주장하지 마세요." + platformShellDirective() + langDirective()
}

// A connected MCP may advertise hundreds of deferred tools. Tell the worker
// which analysis surface is actually available without assuming every run has REA.
func binaryAnalysisToolGuidance(def DeferredInfo) string {
	for _, name := range def.GlobalNames {
		if strings.HasPrefix(strings.ToLower(name), "mcp__rea__") {
			return "\n\n【바이너리 분석 도구 선택】이번 실행에서는 REA MCP를 사용할 수 있습니다. 실행 파일·라이브러리·펌웨어의 구조나 코드 분석이 필요하면 REA의 관련 도구를 먼저 검색하고, 현재 환경과 파일 형식에서 실제 호출 가능한지 확인하세요. REA가 지원하지 않거나 오류가 나면 오류를 기록한 뒤 Capstone, objdump, Ghidra 또는 직접 작성한 분석 스크립트 등 사용 가능한 방법으로 전환하세요. 소스 코드만으로 충분하거나 바이너리 분석이 불필요하다면 REA 호출을 억지로 추가하지 마세요. 보고할 때는 실제 호출한 도구와 결과를 구분하고, 연결·검색만 된 도구를 사용했다고 주장하지 마세요."
		}
	}
	return ""
}

func platformShellDirective() string {
	if runtime.GOOS == "windows" {
		return "\n\n【실행 환경】Bash 도구의 이름과 달리 실제 명령 셸은 Windows PowerShell입니다. Unix heredoc(`python - <<'PY'`), `python3` 별칭, Bash 구문은 사용하지 마세요. Python이 필요하면 `python -c \"...\"`처럼 Windows에서 동작하는 한 줄 명령을 쓰거나 작업 파일을 만든 뒤 `python 파일경로`로 실행하세요. PowerShell 안에서 다시 powershell -Command로 중첩 실행하지 마세요. 셸 구문 오류는 네트워크 차단의 증거가 아닙니다."
	}
	return ""
}

// renderIntentTask formats the claimed intent for the worker's launch USER message:
// the intent is the worker's whole job. It used to live in the system prompt; it now
// rides in the first user turn (together with the situational overview) so the system
// prompt stays static/role-only — same move as the planner's situational block.
// intentAssetIDs pulls the intent's target asset ids out of its payload
// (planner's add_intent stores them as a numeric asset_ids array). nil on absence
// or malformed payload.
func intentAssetIDs(intent *db.Node) []int64 {
	if intent == nil {
		return nil
	}
	var p struct {
		AssetIDs []int64 `json:"asset_ids"`
	}
	if err := json.Unmarshal(intent.Payload, &p); err != nil {
		return nil
	}
	return p.AssetIDs
}

func renderIntentTask(intent *db.Node) string {
	return fmt.Sprintf("\n\n【할당된 의도(이번 실행의 유일한 작업: 이 항목만 수행하고 사실을 기록한 뒤 종료)】:\n%s\n의도 ID: %d (record_fact / report_finding 호출 시 전달)", string(intent.Payload), intent.ID)
}

// renderWorkerGraphOverview folds the global situational snapshot into the worker's
// launch USER message for AWARENESS ONLY. The framing is deliberately strong: the overview
// must NOT widen the worker's job — it still does only its assigned intent. Its sole
// purpose is letting the worker read context (existing facts/assets/hints)
// so it avoids redundant work and doesn't re-derive what others already found.
func renderWorkerGraphOverview(data map[string]any) string {
	// A worker already receives its full assigned intent. Repeating the task,
	// goals, running intents and coverage here made local-model prompts larger
	// than their context window. Retain a short root-task anchor so a narrowly
	// worded intent can still recover prior observations without guessing, plus
	// a small window of findings and hints. Full details remain available via
	// node_detail and worker traces.
	compact := map[string]any{}
	if task, ok := data["task"].(map[string]any); ok {
		root := map[string]string{}
		for key, maxRunes := range map[string]int{"description": 900, "goal": 400} {
			if value, ok := task[key].(string); ok && value != "" {
				runes := []rune(value)
				if len(runes) > maxRunes {
					value = string(runes[:maxRunes]) + "…"
				}
				root[key] = value
			}
		}
		if len(root) > 0 {
			compact["task"] = root
		}
	}
	for _, key := range []string{"recent_facts", "finding_list", "hints"} {
		if entries, ok := data[key].([]map[string]any); ok && len(entries) > 0 {
			if len(entries) > 5 {
				entries = entries[:5]
			}
			compact[key] = entries
		}
	}
	if len(compact) == 0 {
		return ""
	}
	b, err := json.Marshal(compact)
	if err != nil {
		return "" // fall back silently: the worker just won't have the global context
	}
	return "\n\n참고용 작업 요약·기존 사실·취약점·힌트(현재 의도의 범위를 넓히지 않음):\n" + string(b)
}

// Execute runs one intent. hooks (the per-task Guard) gates every tool call; may
// be nil. emit, if non-nil, receives one ActivityRecord per execution step.
// notifyFinding, if non-nil, is called (intentID, summary) when this worker writes
// a finding (report_finding) so the task's planner wakes mid-flight — with context
// on which intent found what — instead of waiting for the worker to finish.
// Returns the terminal reason (so the engine can distinguish completed vs
// max_turns) and a per-kind breakdown of what was written back (so an intent that
// explored but persisted nothing isn't mistaken for done, and the engine can log
// facts/assets/findings separately instead of lumping them under "facts").
func (w *Worker) Execute(ctx context.Context, name string, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string)) (harness.TerminalReason, WriteCounts, error) {
	return w.execute(ctx, name, taskID, as, ts, intent, hooks, emit, enr, notifyFinding, "", "")
}

// ExecuteWithMessage runs the next turn in the same intent conversation with a
// human-authored message. The HTTP handler does not edit the transcript;
// agentcore records the message as a normal user turn when this Worker starts.
// This keeps Worker continuation identical to the regular agent chat flow.
func (w *Worker) ExecuteWithMessage(ctx context.Context, name string, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string), requestID, message string) (harness.TerminalReason, WriteCounts, error) {
	return w.execute(ctx, name, taskID, as, ts, intent, hooks, emit, enr, notifyFinding, strings.TrimSpace(requestID), strings.TrimSpace(message))
}

func (w *Worker) execute(ctx context.Context, name string, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string), requestID, message string) (harness.TerminalReason, WriteCounts, error) {
	tsx := NewToolSet(ts, name)
	tsx.SetFindingRecorder(w.findingRecorder)
	tsx.SetTaskID(taskID)
	coverageEnabled := as == nil || as.CoverageEnabled(taskID)
	tsx.SetCoverageEnabled(coverageEnabled)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetOwnerNode(intent.ID)         // assets this worker discovers anchor to its intent → visible to the task
	tsx.SetEnrich(enr)                  // async DNS/HTTP auto-completion for assets this worker writes
	tsx.SetNotifyFinding(notifyFinding) // report_finding 落库时当场唤醒 planner，带上「哪个意图+finding」
	// base = built-in worker tools ∪ host tools (traffic) ∪ default tools (incl. Bash);
	// then augment with the agent's visible skills/MCP. During the SDK settlement
	// phase, Bash is hidden via Settlement.DisabledTools (no local gating needed).
	base := append(tsx.WorkerTools(), w.extraTools...)
	// worker 刻意不给 MultiEdit/Glob/Grep：文件精改用 Edit、检索走 Bash(grep/find)，
	// 收敛工具面、减少低价值调用。其余 SDK 默认工具(Read/Write/Edit/LS/Bash/Sleep)照常。
	base = append(base, defaultToolsExcept("MultiEdit", "Glob", "Grep")...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts), IntentID: intent.ID})
	tools, def, cleanup := AugmentTools(ctx, "worker", base)
	defer cleanup()

	// 意图是 worker 的【唯一职责、贯穿整个 run 的不变量】→ 连同启动指令、意图锚定的目标资产
	// 原始数据一起放进 system prompt：system 每次 run 都重新拼一遍、绝不会被 compaction 压掉，
	// 长 run 里意图永远在场，续跑时也不依赖 transcript 历史是否留住那条首消息。代价是 system
	// 混入 per-intent 易变数据、失去跨意图缓存复用；这是刻意的取舍（意图丢失比省 token 严重得多）。
	// 与 planner「态势块放 user turn」分叉是有意的：planner 本身是产意图的那个、没有单一 mandate，
	// worker 有。仅【全局态势 overview】留在启动 user 消息里——它可降级、容忍 stale，压掉无碍。
	// 本次意图的专属工作目录 <workDir>/tasks/<taskID>/i<intentID>，引擎侧先建好。
	runDir := ensureRunDir(w.workDir, taskID, intent.ID)
	// The run-wide intent is not the current tool action. Do not forward it or
	// inherit a parent run's background into the action reviewer.
	ctx = intercept.WithReviewContext(ctx, runDir, intercept.ReviewBackground{})
	overview := renderWorkerGraphOverview(tsx.graphOverviewData())
	sysBody := workerSystem(w.proxyAddr, w.proxyCACert, w.workDir, runDir) + binaryAnalysisToolGuidance(def)
	if w.wantConstraints() {
		sysBody += constraintBlock(ts) // 操作约束(若有)注入系统提示,worker 执行时严格遵守
	}
	// 意图块 → 意图锚定资产块 → 启动指令，依次追加到 system 尾部（与 constraintBlock 同一套追加法）。
	sysBody += renderIntentTask(intent)
	if as != nil {
		if ids := intentAssetIDs(intent); len(ids) > 0 {
			if assets, err := as.GetByIDs(ids); err == nil && len(assets) > 0 {
				if b, err := json.Marshal(assets); err == nil {
					sysBody += "\n\n이 의도의 asset_ids에 해당하는 대상 자산:\n" + string(b)
				}
				// 意图明确针对的这些资产 → 自动纳入任务测试范围（与 insertAssets 同一套
				// 保守粒度）。upsertTaskScope 的 ON CONFLICT DO NOTHING + uq_task_scope
				// 唯一索引保证不会重复添加；重跑/重试同样是幂等 no-op。
				// 资产覆盖度功能关闭时不再累积测试范围(分母)。
				if coverageEnabled {
					for _, a := range assets {
						_ = as.AddAutoScope(taskID, a.Type, a.Domain, a.URL, a.IP)
					}
				}
			}
		}
	}
	sysBody += "\n\n위 의도를 실행하세요. 이 의도에 한정해 사실·자산·취약점 결과를 기록하고 완료되면 종료하세요."
	system, boundary := deferredSystem(sysBody, def)
	// 任务级 deadline(经 ctx 注入)夹逼本 run 的墙钟预算 + 决定收尾词(见 taskclock.go)。
	tc := taskClockFrom(ctx)
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, w.runTimeout)
	settle := wrapupSettlement("worker", []string{"Bash"})
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("worker", []string{"Bash"}, clamped)
	}
	opts := agentcore.Options{
		Provider:        w.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		// WebFetch 走记录代理，其 HTTP 与 curl 一样被留痕；载入代理 CA 让经 MITM
		// 重签的 HTTPS 证书能【正常校验通过】（而非关掉校验）。proxy 空则直连。
		EnableWebFetch: true,
		WebFetchProxy:  w.proxyAddr,
		WebFetchCACert: w.proxyCACert,
		// 联网搜索(可选)。ddgs 无需 key；brave-free 需 BraveKey；tavily 需 TavilyKey。
		// WebSearchProxy 是独立的出口代理(http/https/socks5)，与记录流量的 MITM 代理无关；空则直连。
		EnableWebSearch:       w.webSearch.Enabled,
		WebSearchBackend:      w.webSearch.Backend,
		BraveSearchAPIKey:     w.webSearch.BraveKey,
		TavilySearchAPIKey:    w.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: w.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  w.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   w.webSearch.DeepSeekModel,
		WebSearchProxy:        w.webSearch.Proxy,
		// Bash 子命令的 HTTP 默认走记录代理 + 信任其 CA（工具无需 -x/-k）。
		BashEnv:    proxyEnv(w.proxyAddr, w.proxyCACert),
		WorkingDir: runDir,
		MaxTurns:   w.maxTurns, // 0 = unlimited (configurable in agent management)
		// 墙钟预算,轮边界判,不打断半路;0 = 不限。有任务级 deadline 时夹逼到 min(自身预算,
		// 距 deadline 剩余),让本 run 在任务到点时自然进收尾(见 taskclock.go)。
		MaxDuration: maxDur,
		// 命中预算(轮次 OR 时长)→ SDK 跑一轮收尾(隐藏 Bash),把已识别的写回,避免烂尾。
		// clamped(被任务 deadline 夹逼)时用 PromptByReason:因超时=任务到点→任务超时词,
		// 因步数=夹逼窗口内步数先耗尽→回落 per-run 词。非 clamped 维持纯 per-run。
		Settlement: settle,
		// large tool output spills to cmd-output/ with a head + pointer (SDK tool.Capture);
		// full output preserved on disk. 截断上限用 SDK 默认(30000 字符)。
		ToolOutputDir: cmdOutDir(runDir),
		Compaction:    compactionConfig(w.compactionWindow()), // long tool-heavy runs stay within the window
		Todos:         actool.NewTodoStore(),                  // 会话级临时待办（TodoWrite），纯规划用，退出即丢
		NonStreaming:  w.nonStreaming(),                       // 该 profile 选非流式时走 Provider.Complete
		MaxTokens:     w.maxTokens(),                          // 0 = 不发上限,由服务端默认值决定
	}
	if hooks != nil { // typed-nil guard: only set when concrete (avoids harness panic)
		opts.Hooks = hooks
	}
	if w.tx != nil { // persist raw LLM conversation; one file per worked intent
		opts.Transcript = w.tx
		opts.SessionID = WorkerSessionID(ts.ID(), intent.ID)
	}
	intentID := intent.ID
	emitWrap := func(r db.Activity) {
		if emit != nil {
			r.NodeID, r.Worker = &intentID, name
			emit(r)
		}
	}
	// Keep the authoritative intent in the system prompt, but repeat the immediate
	// action in the user turn. Small local models can otherwise treat a graph-only
	// first user message as a request to summarize the graph and return no tools.
	input := "지금 실행할 의도:\n" + renderIntentTask(intent) +
		"\n먼저 필요한 검증 도구를 호출하세요. 입력이 불명확하면 제공된 기존 사실의 id를 node_detail로 확인하거나 get_worker_trace로 이전 실행을 읽으세요. 실제 출력이 없으면 사실이나 취약점을 기록하지 마세요."
	if runtime.GOOS == "windows" {
		input += "\nWindows PowerShell에서는 curl이 Invoke-WebRequest 별칭일 수 있으므로 curl.exe를 명시하거나 Invoke-WebRequest의 정식 구문을 사용하세요."
	}
	// Qwen3's thinking-only replies can consume the output budget without a
	// visible message or tool call. Its documented /no_think switch is scoped
	// to that model family; other providers receive an ordinary user turn.
	if (w.noThinkFn != nil && w.noThinkFn()) || (w.noThinkFn == nil && strings.Contains(strings.ToLower(w.model), "qwen3")) {
		input = "/no_think\n" + input
	}
	if strings.TrimSpace(overview) != "" {
		input += "\n\n참고용 전체 진행 상황:\n" + overview
	}
	input += recentProgressAudit(ts)

	// 实验功能:开启后由 noa 接管上下文压缩(归档集中在 <workDir>/noa/<SessionID> 下,持久)。
	noaSession := WorkerSessionID(ts.ID(), intent.ID)
	enableNoa(&opts, w.noaEnabledFn, w.workDir, noaSession, noaWarn(noaSession))
	ctx = attachSideCapture(ctx, &opts)
	s := agentcore.NewSession(opts)
	defer s.Close() // release the session's background-task manager (temp dir + processes)

	// Resume prior conversation if this intent was paused/blocked/exhausted and is
	// being re-run. The transcript ID is deterministic per intent, so if a prior
	// session exists the worker continues from where it left off instead of
	// restarting from scratch.
	alreadyRecorded := false
	if w.tx != nil {
		_ = s.Resume(opts.SessionID)
		alreadyRecorded = requestID != "" && hasWorkerChatMessage(s.Messages(), requestID)
		if len(s.Messages()) > 0 && message == "" {
			seedUnlockFromHistory(s.Messages(), def.UnlockSkill)
			input = "계속 실행하세요."
		} else if len(s.Messages()) > 0 {
			seedUnlockFromHistory(s.Messages(), def.UnlockSkill)
		}
	}
	if message != "" {
		if alreadyRecorded {
			input = "이전 사용자 대화에서 전달한 새 의도를 계속 실행하세요. 완료한 동작은 반복하지 마세요."
		} else if len(s.Messages()) > 0 {
			input = workerChatMarker(requestID) + "\n【사용자 대화로 전달된 새 의도】\n" + message +
				"\n\n먼저 이 요청을 실행하고, 완료한 뒤 맥락에 따라 기존 작업을 계속할지 판단하세요."
		} else {
			input += "\n\n" + workerChatMarker(requestID) + "\n【사용자 대화로 전달된 새 의도】\n" + message +
				"\n\n이 요청을 우선 실행하세요."
		}
	}

	// Budgets + settlement are owned by the SDK (MaxTurns/MaxDuration + Settlement):
	// on hit it runs a wrap-up turn and finishes with ReasonMaxTurns/ReasonTimeout.
	// MaxDuration now interrupts an in-flight tool at the wall-clock deadline and
	// enters the wrap-up phase on the live ctx, so a run whose tool overran the budget
	// still settles (no external hard-timeout backstop needed). ctx itself carries only
	// pause / planner kill / shutdown, which the engine distinguishes and re-queues/stops.
	_, reason, err := captureRunSession(ctx, s, input, emitWrap)
	return reason, tsx.Writes(), err
}
