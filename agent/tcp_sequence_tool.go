package agent

import (
	"context"
	"encoding/json"
	"fmt"

	actool "github.com/Autumn-27/norma/tool"
)

// tcp_sequence is a discoverable entry point for short-lived, multi-prompt
// services. It uses the same bounded implementation as tcp_session.
func (t *ToolSet) tcpSequence() actool.CoreTool {
	return t.writeExpTool("tcp_sequence", "허가된 원시 TCP 호스트에 새 소켓 하나로 연결합니다. 배너를 읽고 steps의 입력 1~8개를 모델 호출 지연 없이 순서대로 보내며 각 단계의 응답 원문/hex를 반환합니다. 각 step에는 parts 또는 send_text 또는 send_hex 중 하나만 넣으세요. 반복 입력 예시: {\"host\":\"127.0.0.1\",\"port\":9000,\"steps\":[{\"parts\":[{\"repeat_hex\":\"41\",\"count\":88}],\"append_newline\":true},{\"send_text\":\"B\",\"append_newline\":true}]}. count는 정수이며 text에 숫자를 쓰는 것이 아닙니다. 사전에 정할 수 없는 다음 입력은 한 로컬 프로그램에서 응답을 파싱해 처리해야 합니다.",
		obj(map[string]any{
			"host": str("허가된 호스트명 또는 IP"),
			"port": intp("허가된 TCP 포트"),
			"steps": map[string]any{"type": "array", "description": "1~8개 단계. 각 단계에 send_text, send_hex, parts 중 정확히 하나만 지정. 반복 입력은 parts:[{repeat_hex:'41',count:88}], append_newline:true", "items": obj(map[string]any{
				"send_hex": str("보낼 바이트 hex"), "send_text": str("보낼 UTF-8 텍스트"),
				"append_newline": map[string]any{"type": "boolean", "description": "입력 뒤 LF 추가"},
				"parts": map[string]any{"type": "array", "description": "text/hex/repeat_hex+count를 순서대로 조립. 예: [{repeat_hex:'41',count:88}]. parts를 쓰면 send_text/send_hex는 생략", "items": obj(map[string]any{
					"text": str("텍스트"), "hex": str("바이트 hex"), "repeat_hex": str("반복할 바이트 hex"), "count": intp("반복 횟수"),
				})},
				"read_wait_ms": intp("첫 응답 대기 시간 100~3000ms"),
			})},
		}, "host", "port", "steps"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a tcpSessionArgs
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(fmt.Sprintf("잘못된 인자: %v", err)), nil
			}
			a.Action = "open_sequence"
			out, err := t.runTCPSessionEx(ctx, a)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("TCP 단계 실행 실패: %v", err)), nil
			}
			return jsonResult(out)
		})
}
