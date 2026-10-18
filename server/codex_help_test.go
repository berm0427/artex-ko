package server

import (
	"os"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestCodexHelpRequestAsksForActionableExperiment(t *testing.T) {
	message := codexHelpRequestMessage(683)
	for _, want := range []string{"작업 #683", "/api/chat?task=683", "다음 가설", "검증 실험", "원시 응답", "결과별 다음 분기", "허가 범위", "성공으로 판정하지"} {
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
		{`[planner] 작업 683 계획 오류: context canceled`, ""},
	} {
		if got := classifyModelFailure(tc.line); got != tc.want {
			t.Errorf("classifyModelFailure(%q) = %q, want %q", tc.line, got, tc.want)
		}
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
