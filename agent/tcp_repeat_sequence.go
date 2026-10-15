package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
)

// tcp_repeat_sequence deliberately exposes one payload shape. Small local
// models often populate every optional field in tcp_sequence, creating an
// ambiguous payload before any network request is made.
type tcpRepeatStep struct {
	ByteHex       string `json:"byte_hex"`
	Count         int    `json:"count"`
	AppendNewline bool   `json:"append_newline"`
	ReadWaitMS    int    `json:"read_wait_ms"`
}

type tcpRepeatArgs struct {
	Host  string          `json:"host"`
	Port  int             `json:"port"`
	Steps []tcpRepeatStep `json:"steps"`
}

func (t *ToolSet) tcpRepeatSequence() actool.CoreTool {
	return t.writeExpTool("tcp_repeat_sequence", "원시 TCP의 여러 입력 단계를 한 연결에서 수행합니다. 각 단계는 한 바이트를 count번 반복하고 선택적으로 LF를 붙입니다. 예: host, port, steps:[{byte_hex:'41',count:88,append_newline:true},{byte_hex:'42',count:1,append_newline:true}]. 정확한 전송 바이트 수와 응답 hex를 반환합니다. 임의 바이너리 입력은 tcp_sequence를 사용하세요.",
		obj(map[string]any{
			"host": str("허가된 호스트명 또는 IP"),
			"port": intp("허가된 TCP 포트"),
			"steps": map[string]any{"type": "array", "description": "1~8개 반복 바이트 입력 단계", "items": obj(map[string]any{
				"byte_hex":       str("반복할 단일 바이트의 hex. 예: 41"),
				"count":          intp("반복 횟수 1~1024"),
				"append_newline": map[string]any{"type": "boolean", "description": "반복 바이트 뒤 LF 추가"},
				"read_wait_ms":   intp("첫 응답 대기 시간 100~3000ms"),
			}, "byte_hex", "count")},
		}, "host", "port", "steps"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a tcpRepeatArgs
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(fmt.Sprintf("잘못된 인자: %v", err)), nil
			}
			seq := tcpSessionArgs{Action: "open_sequence", Host: a.Host, Port: a.Port}
			for i, step := range a.Steps {
				b, err := hex.DecodeString(strings.TrimSpace(step.ByteHex))
				if err != nil || len(b) != 1 || step.Count < 1 || step.Count > 1024 {
					return actool.Errorf(fmt.Sprintf("steps[%d]: byte_hex는 한 바이트 hex, count는 1~1024여야 합니다", i)), nil
				}
				seq.Steps = append(seq.Steps, tcpSessionStep{Parts: []tcpSessionPart{{RepeatHex: step.ByteHex, Count: step.Count}}, AppendNewline: step.AppendNewline, ReadWaitMS: step.ReadWaitMS})
			}
			out, err := t.runTCPSessionEx(ctx, seq)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("TCP 반복 단계 실행 실패: %v", err)), nil
			}
			return jsonResult(out)
		})
}
