package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/internal/siwc"
	"github.com/Autumn-27/norma/llm"
)

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func main() {
	dir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	st, err := siwc.OpenStore(filepath.Join(dir, "ARTEX", "chatgpt-plan"))
	if err != nil {
		log.Fatal(err)
	}
	if len(os.Args) < 2 {
		log.Fatal("usage: chatgpt-plan-bridge login|serve|status|verify|logout")
	}
	switch os.Args[1] {
	case "login":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		fmt.Println("시스템 브라우저에서 ChatGPT 계정 로그인과 ARTEX 사용 권한을 승인하세요.")
		if err := st.Login(ctx, openBrowser); err != nil {
			log.Fatal(err)
		}
		fmt.Println("ChatGPT plan connected. Run 'serve' to start the local bridge.")
	case "status":
		s := st.Snapshot()
		fmt.Printf("Account: %s\nConnected: %v\nLocal endpoint: http://127.0.0.1:18789/v1\nLocal profile key: %s\n", s.Email, s.AccessToken != "", s.BridgeKey)
	case "serve":
		fmt.Println("ARTEX ChatGPT plan bridge listening on 127.0.0.1:18789")
		if err := http.ListenAndServe("127.0.0.1:18789", &siwc.Bridge{Store: st}); err != nil {
			log.Fatal(err)
		}
	case "verify":
		if err := verify(st); err != nil {
			log.Fatal(err)
		}
	case "logout":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := st.Logout(ctx); err != nil {
			log.Fatal(err)
		}
		fmt.Println("ChatGPT plan 연결을 해제했습니다.")
	default:
		log.Fatal("usage: chatgpt-plan-bridge login|serve|status|verify|logout")
	}
}

// verify exercises the same ARTEX Responses adapter and function-tool schema
// against the subscription bridge. It sends only short benign prompts.
func verify(st *siwc.Store) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	local := httptest.NewServer(&siwc.Bridge{Store: st})
	defer local.Close()
	req, _ := http.NewRequestWithContext(ctx, "GET", local.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+st.Snapshot().BridgeKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("model list HTTP %d: %s", resp.StatusCode, b)
	}
	var catalog struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		return err
	}
	if len(catalog.Data) == 0 {
		return fmt.Errorf("ChatGPT account has no visible models")
	}
	model := catalog.Data[0].ID
	for _, m := range catalog.Data {
		if m.ID == "gpt-6.1-sol" {
			model = m.ID
			break
		}
	}
	fmt.Printf("구독 모델 목록 확인: %d개, 테스트 모델: %s\n", len(catalog.Data), model)
	cfg := agent.ConfigFrom("openai-responses", model, local.URL+"/v1", st.Snapshot().BridgeKey, "")
	provider, err := cfg.NewProvider()
	if err != nil {
		return err
	}
	var toolID, toolArgs string
	for _, tc := range []struct {
		name     string
		req      llm.CompletionRequest
		needTool bool
	}{
		{"text", llm.CompletionRequest{Messages: []llm.Message{llm.UserText("Reply exactly: ARTEX link works.")}}, false},
		{"function", llm.CompletionRequest{System: []string{"Call the supplied ping tool once. No other action."}, Messages: []llm.Message{llm.UserText("Call ping with note ok.")}, Tools: []llm.ToolSchema{{Name: "ping", Description: "Harmless connection test", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"note": map[string]any{"type": "string"}}, "required": []string{"note"}, "additionalProperties": false}}}}, true},
	} {
		completed, tool := false, false
		for ev, err := range provider.Stream(ctx, tc.req) {
			if err != nil {
				return fmt.Errorf("%s request: %w", tc.name, err)
			}
			if ev.Type == llm.SEToolUseStart && ev.ToolName == "ping" {
				tool = true
				toolID = ev.ToolID
			}
			if tc.needTool && ev.Type == llm.SEToolInputJSON {
				toolArgs += ev.Text
			}
			if ev.Type == llm.SEMessageDelta {
				completed = true
			}
		}
		if !completed || (tc.needTool && !tool) {
			return fmt.Errorf("%s request did not complete with the expected result (completed=%v tool=%v)", tc.name, completed, tool)
		}
		fmt.Printf("%s 추론 완료\n", tc.name)
	}
	if toolID == "" {
		return fmt.Errorf("function call had no call ID")
	}
	if toolArgs == "" {
		toolArgs = `{}`
	}
	followup := llm.CompletionRequest{Messages: []llm.Message{
		llm.UserText("Call ping with note ok."),
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: toolID, Name: "ping", Input: json.RawMessage(toolArgs)}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText(toolID, "pong", false)}},
		llm.UserText("Now reply exactly: pong received."),
	}}
	completed, textSeen := false, false
	for ev, err := range provider.Stream(ctx, followup) {
		if err != nil {
			return fmt.Errorf("tool result replay: %w", err)
		}
		if ev.Type == llm.SETextDelta && ev.Text != "" {
			textSeen = true
		}
		if ev.Type == llm.SEMessageDelta {
			completed = true
		}
	}
	if !completed || !textSeen {
		return fmt.Errorf("tool result replay did not complete (completed=%v text=%v)", completed, textSeen)
	}
	fmt.Println("도구 결과 재입력 완료")
	return nil
}
