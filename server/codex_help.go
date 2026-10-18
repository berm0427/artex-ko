package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// codexHelpConfig connects ARTEX to an existing Codex conversation. It is
// deliberately local and opt-in: no new model session or scheduled automation.
type codexHelpConfig struct {
	ThreadID string `json:"thread_id"`
	CLIPath  string `json:"cli_path"`
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
	if !validCodexThreadID(cfg.ThreadID) || cfg.CLIPath == "" {
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

func codexHelpRequestMessage(id int64) string {
	return fmt.Sprintf("ARTEX 도움 요청: 작업 #%d의 목표가 열린 채 새 의도 없이 멈췄습니다. 로컬 ARTEX의 목표·제약·최근 실험과 실패 응답을 확인하세요. 단순히 질문에만 답하거나 '더 분석하라'는 일반론으로 끝내지 말고, 확인된 사실과 추론을 구분해 왜 멈췄는지 설명하고, 중복되지 않는 다음 가설·실제 수행 가능한 한 가지 검증 실험·예상되는 원시 응답 또는 관찰값·결과별 다음 분기를 구체적으로 제안해 주세요. 국소적인 결함 검증에 목표 출력의 사전 증명까지 요구하여 모든 실험을 중단시키지 마세요. 근거가 있으면 기존 ARTEX /api/chat?task=%d 경로로 충분한 맥락과 함께 전달하고 이 대화 사용자에게 알리세요. 근거가 없으면 억지 실험이나 성공을 꾸미지 말고 한계를 보고하세요. 작업의 허가 범위를 지키고 실제 대상 응답 없이 성공으로 판정하지 마세요.", id, id)
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
		if state.Paused || state.Queued || isTerminalStatus(state.Status) || sc.s.engine.IsDeleting(task.ID) || sc.s.engine.ActiveLLMCalls(task.ID) > 0 {
			continue
		}
		active, err := task.Store.HasActiveIntent()
		if err != nil || active {
			continue
		}
		open, err := task.Store.HasOpenGoal()
		if err != nil || !open {
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
		if marker != "" {
			requestedAt, parseErr := strconv.ParseInt(marker, 10, 64)
			if parseErr != nil {
				continue
			}
			// A later, completed experiment earns another review. Planner
			// chatter and repeated status messages do not.
			var latestIntent int64
			if err := sc.pg.QueryRow(`SELECT COALESCE(EXTRACT(EPOCH FROM MAX(created_at))::bigint, 0)
FROM exploration_nodes WHERE exploration_id=$1 AND kind='intent'`, task.ExpID).Scan(&latestIntent); err != nil || latestIntent <= requestedAt || now-latestIntent < 90 {
				continue
			}
		}
		// Never include task data, target addresses or secrets in the CLI
		// argument. The Codex assistant reads the task record itself.
		message := codexHelpRequestMessage(id)
		if err := queueCodexHelp(cfg, message); err != nil {
			log.Printf("[codex-help] task=%d queue failed: %v", id, err)
			continue
		}
		if err := sc.pg.SetSchedState(key, strconv.FormatInt(now, 10)); err != nil {
			log.Printf("[codex-help] task=%d marker failed: %v", id, err)
			continue
		}
		log.Printf("[codex-help] task=%d queued to existing Codex conversation", id)
	}
}
