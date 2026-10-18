package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestCodexHelpRequestAsksForActionableExperiment(t *testing.T) {
	message := codexHelpRequestMessage(683, `C:\repo\scripts\send-codex-help.ps1`)
	for _, want := range []string{"작업 #683", "/api/chat?task=683", "다음 가설", "검증 실험", "원시 응답", "결과별 다음 분기", "허가 범위", "성공으로 판정하지", "send-codex-help.ps1"} {
		if !strings.Contains(message, want) {
			t.Errorf("help request missing %q", want)
		}
	}
}

func TestValidCodexThreadID(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"01a0a0ab-a203-75e1-a1ed-2016eaf8e8ed", true},
		{"", false},
		{"not-a-thread", false},
		{"01a0a0ab-a203-75e1-a1ed-2016eaf8e8eg", false},
	} {
		if got := validCodexThreadID(tc.id); got != tc.want {
			t.Errorf("validCodexThreadID(%q) = %t, want %t", tc.id, got, tc.want)
		}
	}
}

func TestClassifyModelFailure(t *testing.T) {
	for _, tc := range []struct {
		line string
		want string
	}{
		{`[planner] 작업 683 계획 오류: openai-responses: status 503: Post "https://auth.openai.com/api/accounts/oauth/token": dial tcp: lookup auth.openai.com: no such host`, "auth_dns"},
		{`[planner] 작업 683 계획 오류: openai-responses: status 401: invalid local bridge key`, "bridge_key"},
		{`[planner] 작업 683 계획 오류: openai-responses: status 401: {"code":"token_revoked"}`, "auth_revoked"},
		{`[planner] 작업 683 계획 오류: context canceled`, ""},
	} {
		if got := classifyModelFailure(tc.line); got != tc.want {
			t.Errorf("classifyModelFailure(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestCodexHelpShouldRequest(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		now, requested, intent, reply int64
		retries                       int
		send, retry                   bool
	}{
		{"first", 1000, 0, 100, 0, 0, true, false},
		{"wait for answer", 1300, 1000, 900, 0, 0, false, true},
		{"retry unanswered", 1600, 1000, 900, 0, 0, true, true},
		{"acknowledged", 1900, 1000, 900, 1100, 0, false, false},
		{"bounded", 2200, 1000, 900, 0, 2, false, true},
		{"new experiment", 1700, 1000, 1500, 1100, 2, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			send, retry := codexHelpShouldRequest(tc.now, tc.requested, tc.intent, tc.reply, tc.retries)
			if send != tc.send || retry != tc.retry {
				t.Fatalf("got (%v,%v), want (%v,%v)", send, retry, tc.send, tc.retry)
			}
		})
	}
}

func TestCodexHelpReplyOnlyAllowsLocalTaskChat(t *testing.T) {
	dir := t.TempDir()
	key := strings.Repeat("x", 43)
	if err := os.WriteFile(filepath.Join(dir, "codex-help.json"), []byte(`{"reply_key":"`+key+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: &Manager{dir: dir}}
	h := s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		name, method, path, remote, key string
		want                            int
	}{
		{"local chat", "POST", "/api/chat?task=683", "127.0.0.1:1234", key, 204},
		{"wrong key", "POST", "/api/chat?task=683", "127.0.0.1:1234", "bad", 401},
		{"remote", "POST", "/api/chat?task=683", "192.0.2.1:1234", key, 401},
		{"other route", "POST", "/api/tasks", "127.0.0.1:1234", key, 401},
		{"missing task", "POST", "/api/chat", "127.0.0.1:1234", key, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			r.RemoteAddr = tc.remote
			r.Header.Set(codexHelpReplyHeader, tc.key)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("HTTP %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestTaskModelFailureIsolatedDB(t *testing.T) {
	dsn := os.Getenv("ARTEX_PG_DSN")
	if dsn == "" {
		t.Skip("isolated ARTEX_PG_DSN not configured")
	}
	store, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sc := &Scheduler{pg: store}
	const id int64 = 987654321
	if _, err := store.InsertLog("info", "planner", `[planner] 작업 987654321 계획 오류: lookup auth.openai.com: no such host`); err != nil {
		t.Fatal(err)
	}
	if logID, kind := sc.taskModelFailure(id); logID == 0 || kind != "auth_dns" {
		t.Fatalf("failure = (%d, %q), want nonzero auth_dns", logID, kind)
	}
	if _, err := store.InsertLog("info", "planner", `[planner] 작업 987654321 계획 완료`); err != nil {
		t.Fatal(err)
	}
	if logID, kind := sc.taskModelFailure(id); logID != 0 || kind != "" {
		t.Fatalf("recovered = (%d, %q), want empty", logID, kind)
	}
}
