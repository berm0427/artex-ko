package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const codexHelpReplyHeader = "X-ARTEX-Codex-Help-Key"

// Only the existing task chat may be reached with the local help key. The key
// never authorizes task management, settings or requests from another host.
func (s *Server) validCodexHelpReply(r *http.Request) bool {
	if r.Method != http.MethodPost || r.URL.Path != "/api/chat" || s.m == nil {
		return false
	}
	if id, err := strconv.ParseInt(r.URL.Query().Get("task"), 10, 64); err != nil || id <= 0 {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return false
	}
	provided := r.Header.Get(codexHelpReplyHeader)
	if len(provided) != 43 {
		return false
	}
	data, err := os.ReadFile(filepath.Join(s.m.dir, "codex-help.json"))
	if err != nil {
		return false
	}
	var cfg codexHelpConfig
	if json.Unmarshal(data, &cfg) != nil || len(cfg.ReplyKey) != 43 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(cfg.ReplyKey)) == 1
}

// codexHelpConfig connects ARTEX to an existing Codex conversation. It is
// deliberately local and opt-in: no new model session or scheduled automation.
type codexHelpConfig struct {
	ThreadID string `json:"thread_id"`
	CLIPath  string `json:"cli_path"`
	ReplyKey string `json:"reply_key"`
}

func (sc *Scheduler) codexHelpConfig() (codexHelpConfig, error) {
	path := filepath.Join(sc.s.m.dir, "codex-help.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return codexHelpConfig{}, err
	}
	var cfg codexHelpConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if !validCodexThreadID(cfg.ThreadID) || cfg.CLIPath == "" || len(cfg.ReplyKey) != 43 {
		return cfg, fmt.Errorf("invalid Codex help configuration")
	}
	if info, err := os.Stat(cfg.CLIPath); err != nil || info.IsDir() {
		return cfg, fmt.Errorf("Codex executable is unavailable")
	}
	return cfg, nil
}

func validCodexThreadID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// taskModelFailure returns only a classified operational failure, never the raw
// provider error (which may contain credentials or request data). A later
// successful planner round clears the failure.
func (sc *Scheduler) taskModelFailure(id int64) (logID int64, kind string) {
	var line string
	err := sc.pg.QueryRow(`SELECT id, text FROM server_logs
WHERE tag='planner' AND text LIKE $1 ORDER BY id DESC LIMIT 1`,
		fmt.Sprintf("[planner] 작업 %d 계획 %%", id)).Scan(&logID, &line)
	if err != nil || !strings.Contains(line, "계획 오류:") {
		return 0, ""
	}
	return logID, classifyModelFailure(line)
}

func classifyModelFailure(line string) string {
	s := strings.ToLower(line)
	switch {
	case strings.Contains(s, "lookup auth.openai.com") &&
		(strings.Contains(s, "no such host") || strings.Contains(s, "server misbehaving")):
		return "auth_dns"
	case strings.Contains(s, "invalid local bridge key"):
		return "bridge_key"
	case strings.Contains(s, "token_revoked") || strings.Contains(s, "invalidated oauth token"):
		return "auth_revoked"
	default:
		return ""
	}
}

// handleTaskModelFailure shares an infrastructure diagnosis with the connected
// Codex conversation and wakes the original task after DNS recovers. It never
// creates a successor task or passes the unredacted provider error to Codex.
func (sc *Scheduler) handleTaskModelFailure(task *Task, cfg codexHelpConfig) bool {
	id, err := strconv.ParseInt(task.ID, 10, 64)
	if err != nil {
		return false
	}
	logID, kind := sc.taskModelFailure(id)
	if kind == "" {
		return false
	}
	key := fmt.Sprintf("codex_help_model_error_%d", id)
	marker, err := sc.pg.GetSchedState(key)
	if err != nil {
		return true
	}
	if marker != strconv.FormatInt(logID, 10) {
		message := fmt.Sprintf("ARTEX 운영 오류: 작업 #%d의 모델 호출이 실패했습니다. ", id)
		switch kind {
		case "auth_dns":
			message += "auth.openai.com DNS 조회 실패입니다. 풀이 실패가 아니므로 새 공격 가설이나 후속 작업을 만들지 말고 네트워크/DNS 복구를 확인해 주세요. 원본 작업은 복구 시 다시 깨웁니다."
		case "bridge_key":
			message += "로컬 브리지 키 불일치입니다. 키 원문은 공유하지 말고 브리지 연결·설정의 일치 여부를 점검해 주세요."
		case "auth_revoked":
			message += "ChatGPT OAuth 연결이 해제되었습니다. 풀이 실패가 아니므로 새 공격 가설을 만들지 말고 사용자의 재로그인과 브리지 재기동을 기다려 주세요."
		}
		if err := queueCodexHelp(cfg, message); err != nil {
			log.Printf("[codex-help] task=%d model error queue failed: %v", id, err)
		} else {
			_ = sc.pg.SetSchedState(key, strconv.FormatInt(logID, 10))
			log.Printf("[codex-help] task=%d operational failure %s shared", id, kind)
		}
	}
	if kind == "auth_dns" {
		recoveredKey := fmt.Sprintf("codex_help_model_recovered_%d", id)
		recovered, _ := sc.pg.GetSchedState(recoveredKey)
		if recovered != strconv.FormatInt(logID, 10) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_, lookupErr := net.DefaultResolver.LookupHost(ctx, "auth.openai.com")
			cancel()
			if lookupErr == nil {
				_ = sc.pg.SetSchedState(recoveredKey, strconv.FormatInt(logID, 10))
				task.Notify()
				log.Printf("[codex-help] task=%d auth DNS recovered; original task notified", id)
			}
		}
	}
	return true
}

func queueCodexHelp(cfg codexHelpConfig, message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.CLIPath, "queue", "--thread", cfg.ThreadID, "--message", message)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func codexHelpRequestMessage(id int64, scriptPath string) string {
	return fmt.Sprintf("ARTEX 도움 요청: 작업 #%d의 목표가 열린 채 새 의도 없이 멈췄습니다. 로컬 ARTEX의 목표·제약·최근 실험과 실패 응답을 확인하세요. 확인된 사실과 추론을 구분해 왜 멈췄는지 설명하고, 중복되지 않는 다음 가설·수행 가능한 검증 실험 하나·예상 원시 응답·결과별 다음 분기를 제안해 주세요. 국소 결함 검증에 목표 출력의 사전 증명까지 요구해 모든 실험을 중단시키지 마세요. 근거가 있으면 검토한 조언을 표준 입력으로 %q -TaskId %d 에 전달하세요. 이 스크립트가 로컬 인증으로 ARTEX /api/chat?task=%d 에 전송합니다. 인증 키는 출력하거나 대화에 붙이지 마세요. 사용자에게도 결과를 알리세요. 근거가 없으면 억지 실험이나 성공을 꾸미지 말고 한계를 보고하세요. 작업의 허가 범위를 지키고 실제 대상 응답 없이 성공으로 판정하지 마세요.", id, scriptPath, id, id)
}

// A queued CLI message is not an answer. Retry a silent request at most twice,
// and stop immediately once a reviewed response reaches the task chat.
func codexHelpShouldRequest(now, requestedAt, latestIntent, latestReply int64, retries int) (send, retry bool) {
	if requestedAt == 0 {
		return true, false
	}
	if latestIntent > requestedAt && latestIntent > latestReply {
		return now-latestIntent >= 90, false
	}
	if latestReply > requestedAt {
		return false, false
	}
	return retries < 2 && now-requestedAt >= 600, true
}

// Active workers are not automatically interrupted. After several completed
// intents in a short window, ask Codex to review whether they represent real
// progress or a loop. Only a reviewed reply may advise the task through chat.
func codexHelpActiveReviewDue(now, latestDone, previousDone, previousAt int64, recentDone, newDone int) bool {
	return latestDone > 0 && recentDone >= 4 &&
		(previousDone == 0 || (latestDone > previousDone && newDone >= 3)) &&
		(previousAt == 0 || now-previousAt >= 600)
}

func (sc *Scheduler) codexHelpActiveReview(task *Task, cfg codexHelpConfig, id, now int64) {
	var latestDone, recentDone int64
	if err := sc.pg.QueryRow(`SELECT COALESCE(MAX(id),0), COUNT(*) FROM exploration_nodes
WHERE exploration_id=$1 AND kind='intent' AND state='done'
AND completed_at > NOW() - INTERVAL '45 minutes'`, task.ExpID).Scan(&latestDone, &recentDone); err != nil {
		return
	}
	key := fmt.Sprintf("codex_help_active_review_%d", id)
	marker, err := sc.pg.GetSchedState(key)
	if err != nil {
		return
	}
	var previousDone, previousAt int64
	_, _ = fmt.Sscanf(marker, "%d:%d", &previousDone, &previousAt)
	var newDone int64
	if previousDone > 0 {
		if err := sc.pg.QueryRow(`SELECT COUNT(*) FROM exploration_nodes
WHERE exploration_id=$1 AND kind='intent' AND state='done' AND id>$2
AND completed_at > NOW() - INTERVAL '45 minutes'`, task.ExpID, previousDone).Scan(&newDone); err != nil {
			return
		}
	}
	if !codexHelpActiveReviewDue(now, latestDone, previousDone, previousAt, int(recentDone), int(newDone)) {
		return
	}
	message := fmt.Sprintf("ARTEX 진행 중 검토 요청: 작업 #%d에서 최근 45분 동안 완료된 의도가 %d개입니다. 진행 중인 의도를 자동 중단하지 말고, 최근 의도·원시 관찰·새 증거를 비교해 실제 진전인지 반복인지 판단해 주세요. 반복이라고 확인된 경우에만 다른 가설과 구별 가능한 단일 검증을 제안하고, 검토한 조언을 표준 입력으로 %q -TaskId %d 에 전달하세요. 이 스크립트가 로컬 인증으로 ARTEX /api/chat?task=%d 에 전송합니다. 인증 키는 출력하지 마세요. 서로 다른 유효한 실험이라면 추가 의도를 강요하지 말고 그 이유를 사용자에게 알리세요. 허가 범위를 지키고 실제 대상 응답 없이 성공으로 판정하지 마세요.", id, recentDone, filepath.Join(filepath.Dir(sc.s.m.dir), "scripts", "send-codex-help.ps1"), id, id)
	if err := queueCodexHelp(cfg, message); err != nil {
		log.Printf("[codex-help] task=%d active review queue failed: %v", id, err)
		return
	}
	if err := sc.pg.SetSchedState(key, fmt.Sprintf("%d:%d", latestDone, now)); err != nil {
		log.Printf("[codex-help] task=%d active review marker failed: %v", id, err)
		return
	}
	log.Printf("[codex-help] task=%d active progress review queued", id)
}

// codexHelpStep is event-driven by ARTEX's own scheduler tick. A request only
// goes out when an unfinished task has been idle without an active intent.
// The Codex conversation inspects the local evidence and may reply through
// ARTEX's existing /api/chat route; ARTEX retains control of experiments.
func (sc *Scheduler) codexHelpStep() {
	cfg, err := sc.codexHelpConfig()
	if err != nil {
		return // No local connection configured.
	}
	now := time.Now().Unix()
	if now%30 >= int64(sc.tick/time.Second) {
		return
	}
	tasks := sc.s.m.List()
	floorText, err := sc.pg.GetSchedState("codex_help_first_task")
	if err != nil {
		return
	}
	if floorText == "" {
		var newest int64
		for _, task := range tasks {
			id, parseErr := strconv.ParseInt(task.ID, 10, 64)
			if parseErr == nil && id > newest {
				newest = id
			}
		}
		floorText = strconv.FormatInt(newest, 10)
		if err := sc.pg.SetSchedState("codex_help_first_task", floorText); err != nil {
			return
		}
	}
	floor, _ := strconv.ParseInt(floorText, 10, 64)
	for _, task := range tasks {
		id, err := strconv.ParseInt(task.ID, 10, 64)
		if err != nil || id < floor {
			continue
		}
		state := task.lifecycleSnapshot()
		if state.Paused || state.Queued || isTerminalStatus(state.Status) || sc.s.engine.IsDeleting(task.ID) {
			continue
		}
		active, err := task.Store.HasActiveIntent()
		if err != nil {
			continue
		}
		open, err := task.Store.HasOpenGoal()
		if err != nil || !open {
			continue
		}
		if active {
			sc.codexHelpActiveReview(task, cfg, id, now)
			continue
		}
		if sc.s.engine.ActiveLLMCalls(task.ID) > 0 {
			continue
		}
		// Planner status chatter is not progress. Once a task has existed
		// long enough with no active experiment, request outside review even
		// if the planner keeps emitting identical results every few minutes.
		if task.CreatedAt == 0 || now-task.CreatedAt < 180 {
			continue
		}
		if sc.handleTaskModelFailure(task, cfg) {
			continue // Infrastructure failures are not exploration stalls.
		}
		key := fmt.Sprintf("codex_help_requested_%d", id)
		marker, err := sc.pg.GetSchedState(key)
		if err != nil {
			continue
		}
		requestedAt, _ := strconv.ParseInt(marker, 10, 64)
		var latestIntent, latestReply int64
		if err := sc.pg.QueryRow(`SELECT COALESCE(EXTRACT(EPOCH FROM MAX(created_at))::bigint, 0)
FROM exploration_nodes WHERE exploration_id=$1 AND kind='intent'`, task.ExpID).Scan(&latestIntent); err != nil {
			continue
		}
		if err := sc.pg.QueryRow(`SELECT COALESCE(EXTRACT(EPOCH FROM MAX(created_at))::bigint, 0)
FROM activity WHERE exploration_id=$1 AND worker='mainagent' AND kind='user' AND (summary LIKE $2 OR summary LIKE $3)`,
			task.ExpID, fmt.Sprintf("CODEX_HELP_%d:%%", id), fmt.Sprintf("Codex%%#%d:%%", id)).Scan(&latestReply); err != nil {
			continue
		}
		retryKey := fmt.Sprintf("codex_help_retries_%d", id)
		retryText, err := sc.pg.GetSchedState(retryKey)
		if err != nil {
			continue
		}
		retries, _ := strconv.Atoi(retryText)
		send, retry := codexHelpShouldRequest(now, requestedAt, latestIntent, latestReply, retries)
		if !send {
			continue
		}
		// Never include task data, target addresses or secrets in the CLI
		// argument. The Codex assistant reads the task record itself.
		message := codexHelpRequestMessage(id, filepath.Join(filepath.Dir(sc.s.m.dir), "scripts", "send-codex-help.ps1"))
		if retry {
			message = fmt.Sprintf("이전 도움 요청이 ARTEX 채팅에 전달되지 않아 재요청합니다 (%d/2). ", retries+1) + message
		}
		if err := queueCodexHelp(cfg, message); err != nil {
			log.Printf("[codex-help] task=%d queue failed: %v", id, err)
			continue
		}
		if err := sc.pg.SetSchedState(key, strconv.FormatInt(now, 10)); err != nil {
			log.Printf("[codex-help] task=%d marker failed: %v", id, err)
			continue
		}
		if retry {
			_ = sc.pg.SetSchedState(retryKey, strconv.Itoa(retries+1))
		} else {
			_ = sc.pg.SetSchedState(retryKey, "0")
		}
		log.Printf("[codex-help] task=%d queued to existing Codex conversation", id)
	}
}
