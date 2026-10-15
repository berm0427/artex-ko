package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	actool "github.com/Autumn-27/norma/tool"
)

// tcpSession keeps one bounded raw TCP connection for a single worker run.
// Unlike tcp_exchange, it can follow a prompt, inspect its reply, and then
// adapt the next input without losing the server-side state.
type tcpSessionPart struct {
	Text      string `json:"text"`
	Hex       string `json:"hex"`
	RepeatHex string `json:"repeat_hex"`
	Count     int    `json:"count"`
}

type tcpSessionArgs struct {
	Action        string           `json:"action"`
	Host          string           `json:"host"`
	Port          int              `json:"port"`
	SendHex       string           `json:"send_hex"`
	SendText      string           `json:"send_text"`
	AppendNewline bool             `json:"append_newline"`
	Parts         []tcpSessionPart `json:"parts"`
	ReadWaitMS    int              `json:"read_wait_ms"`
	Steps         []tcpSessionStep `json:"steps"`
}

type tcpSessionStep struct {
	SendHex       string           `json:"send_hex"`
	SendText      string           `json:"send_text"`
	AppendNewline bool             `json:"append_newline"`
	Parts         []tcpSessionPart `json:"parts"`
	ReadWaitMS    int              `json:"read_wait_ms"`
}

func (t *ToolSet) tcpSession() actool.CoreTool {
	return t.writeExpTool("tcp_session", "대화형 원시 TCP 서비스용 도구입니다. open으로 초기 출력, open_send로 새 연결의 배너와 즉시 첫 입력의 응답, open_sequence로 미리 정한 여러 입력과 각 응답을 같은 연결·호출에서 확인합니다. send/read/close로 남은 연결을 다룹니다. 짧은 서버 입력 제한 시간에는 별도 호출 사이에 소켓이 닫힐 수 있으므로 open_send/open_sequence를 사용하세요. send_text는 UTF-8, send_hex는 바이너리 hex, parts는 text/hex/repeat_hex+count 조각입니다. 워커마다 연결 하나, 5분 유휴 자동 종료입니다.",
		obj(map[string]any{
			"action":         str("open, open_send, open_sequence, send, read, close 중 하나"),
			"host":           str("open/open_send/open_sequence 시 허가된 호스트"),
			"port":           intp("open/open_send/open_sequence 시 허가된 포트"),
			"send_hex":       str("send 시 바이트 hex; 0x 접두사 없음"),
			"send_text":      str("send 시 UTF-8 텍스트"),
			"append_newline": map[string]any{"type": "boolean", "description": "send_text 또는 parts 뒤에 LF(0a)를 하나 추가"},
			"parts":          map[string]any{"type": "array", "description": "선택: 순서대로 조립할 바이트 조각. 각 항목은 text, hex 또는 repeat_hex+count 중 하나", "items": obj(map[string]any{"text": str("UTF-8 텍스트"), "hex": str("바이트 hex"), "repeat_hex": str("반복할 바이트 hex"), "count": intp("반복 횟수")})},
			"read_wait_ms":   intp("open_send/send/read에서 첫 응답을 기다릴 시간(100~3000ms, 기본 2000ms)"),
			"steps": map[string]any{"type": "array", "description": "open_sequence 전용: 사전에 정한 1~8개 입력을 한 소켓에서 순서대로 즉시 전송", "items": obj(map[string]any{
				"send_hex": str("바이트 hex"), "send_text": str("UTF-8 텍스트"), "append_newline": map[string]any{"type": "boolean"},
				"parts":        map[string]any{"type": "array", "items": obj(map[string]any{"text": str("텍스트"), "hex": str("hex"), "repeat_hex": str("반복 hex"), "count": intp("반복 횟수")})},
				"read_wait_ms": intp("첫 응답 대기 ms"),
			})},
		}, "action"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a tcpSessionArgs
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(fmt.Sprintf("잘못된 인자: %v", err)), nil
			}
			out, err := t.runTCPSessionEx(ctx, a)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("TCP 세션 실패: %v", err)), nil
			}
			return jsonResult(out)
		})
}

func (t *ToolSet) runTCPSession(ctx context.Context, action, host string, port int, sendHex string) (map[string]any, error) {
	return t.runTCPSessionEx(ctx, tcpSessionArgs{Action: action, Host: host, Port: port, SendHex: sendHex})
}

func (t *ToolSet) runTCPSessionEx(ctx context.Context, a tcpSessionArgs) (map[string]any, error) {
	t.tcpMu.Lock()
	defer t.tcpMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(a.Action))
	switch action {
	case "open", "open_send", "open_sequence":
		host := strings.TrimSpace(a.Host)
		if host == "" || strings.ContainsAny(host, "/\\\r\n\t ") || a.Port < 1 || a.Port > 65535 {
			return nil, errors.New("유효한 호스트와 TCP 포트가 필요합니다")
		}
		var payload []byte
		if action == "open_send" {
			var err error
			payload, err = tcpSessionPayload(a)
			if err != nil {
				return nil, err
			}
		}
		var sequence [][]byte
		if action == "open_sequence" {
			if len(a.Steps) < 1 || len(a.Steps) > 8 {
				return nil, errors.New("steps는 1~8개여야 합니다")
			}
			for i, step := range a.Steps {
				data, err := tcpSessionPayload(tcpSessionArgs{SendHex: step.SendHex, SendText: step.SendText, AppendNewline: step.AppendNewline, Parts: step.Parts})
				if err != nil {
					return nil, fmt.Errorf("steps[%d] (단계 %d): %w", i, i+1, err)
				}
				sequence = append(sequence, data)
			}
		}
		t.closeTCPSessionLocked()
		conn, err := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(a.Port)))
		if err != nil {
			return nil, err
		}
		t.tcpConn = conn
		t.resetTCPSessionExpiryLocked()
		deadline := time.Now().Add(5 * time.Second)
		state, err := readTCPBurstState(conn, deadline, time.Until(deadline))
		if err != nil {
			t.closeTCPSessionLocked()
			return nil, err
		}
		if state.PeerClosed {
			t.closeTCPSessionLocked()
			return tcpSessionResult(action, state, ""), nil
		}
		if action == "open_sequence" {
			out := tcpSessionResult(action, state, "")
			out["initial_hex"] = hex.EncodeToString(state.Data)
			out["initial_text"] = tcpSessionText(state.Data)
			out["initial_bytes"] = len(state.Data)
			results := make([]map[string]any, 0, len(sequence))
			for i, data := range sequence {
				if err := ctx.Err(); err != nil {
					t.closeTCPSessionLocked()
					return nil, err
				}
				deadline = time.Now().Add(4 * time.Second)
				_ = conn.SetWriteDeadline(deadline)
				if _, err := conn.Write(data); err != nil {
					t.closeTCPSessionLocked()
					return nil, fmt.Errorf("steps[%d] 전송: %w", i, err)
				}
				state, err = readTCPBurstState(conn, deadline, tcpSessionReadWait(a.Steps[i].ReadWaitMS))
				if err != nil {
					t.closeTCPSessionLocked()
					return nil, fmt.Errorf("steps[%d] 수신: %w", i, err)
				}
				result := tcpSessionResult("send", state, hex.EncodeToString(data))
				result["step"] = i + 1
				results = append(results, result)
				if state.PeerClosed {
					t.closeTCPSessionLocked()
					break
				}
			}
			out["steps"] = results
			out["peer_closed"] = state.PeerClosed
			if !state.PeerClosed {
				t.resetTCPSessionExpiryLocked()
			}
			return out, nil
		}
		if action == "open_send" {
			initial := state
			deadline = time.Now().Add(4 * time.Second)
			_ = conn.SetWriteDeadline(deadline)
			if _, err := conn.Write(payload); err != nil {
				t.closeTCPSessionLocked()
				return nil, err
			}
			state, err = readTCPBurstState(conn, deadline, tcpSessionReadWait(a.ReadWaitMS))
			if err != nil {
				t.closeTCPSessionLocked()
				return nil, err
			}
			out := tcpSessionResult(action, state, hex.EncodeToString(payload))
			out["initial_hex"] = hex.EncodeToString(initial.Data)
			out["initial_text"] = tcpSessionText(initial.Data)
			out["initial_bytes"] = len(initial.Data)
			if state.PeerClosed {
				t.closeTCPSessionLocked()
			} else {
				t.resetTCPSessionExpiryLocked()
			}
			return out, nil
		}
		return tcpSessionResult(action, state, ""), nil
	case "send":
		if t.tcpConn == nil {
			return nil, errors.New("먼저 action=open으로 연결하세요")
		}
		payload, err := tcpSessionPayload(a)
		if err != nil {
			return nil, err
		}
		wait := tcpSessionReadWait(a.ReadWaitMS)
		deadline := time.Now().Add(4 * time.Second)
		_ = t.tcpConn.SetDeadline(deadline)
		if _, err := t.tcpConn.Write(payload); err != nil {
			t.closeTCPSessionLocked()
			return nil, err
		}
		state, err := readTCPBurstState(t.tcpConn, deadline, wait)
		if err != nil {
			t.closeTCPSessionLocked()
			return nil, err
		}
		if state.PeerClosed {
			t.closeTCPSessionLocked()
		} else {
			t.resetTCPSessionExpiryLocked()
		}
		return tcpSessionResult(action, state, hex.EncodeToString(payload)), nil
	case "read":
		if t.tcpConn == nil {
			return nil, errors.New("먼저 action=open으로 연결하세요")
		}
		deadline := time.Now().Add(4 * time.Second)
		state, err := readTCPBurstState(t.tcpConn, deadline, tcpSessionReadWait(a.ReadWaitMS))
		if err != nil {
			t.closeTCPSessionLocked()
			return nil, err
		}
		if state.PeerClosed {
			t.closeTCPSessionLocked()
		} else {
			t.resetTCPSessionExpiryLocked()
		}
		return tcpSessionResult(action, state, ""), nil
	case "close":
		t.closeTCPSessionLocked()
		return map[string]any{"action": action, "closed": true}, nil
	default:
		return nil, errors.New("action은 open, open_send, open_sequence, send, read, close 중 하나여야 합니다")
	}
}

func tcpSessionResult(action string, state tcpReadState, sentHex string) map[string]any {
	data := state.Data
	return map[string]any{"action": action, "sent_hex": sentHex,
		"sent_bytes":     len(sentHex) / 2,
		"received_bytes": len(data), "received_hex": hex.EncodeToString(data), "received_text": tcpSessionText(data),
		"peer_closed": state.PeerClosed, "idle_timeout": state.TimedOut}
}

func tcpSessionText(data []byte) string {
	text := string(data)
	if !utf8.Valid(data) {
		text = strings.ToValidUTF8(text, "�")
	}
	return text
}

func tcpSessionReadWait(ms int) time.Duration {
	if ms == 0 {
		ms = 2000
	}
	if ms < 100 {
		ms = 100
	}
	if ms > 3000 {
		ms = 3000
	}
	return time.Duration(ms) * time.Millisecond
}

func tcpSessionPayload(a tcpSessionArgs) ([]byte, error) {
	configured := 0
	if a.SendHex != "" {
		configured++
	}
	if a.SendText != "" {
		configured++
	}
	if len(a.Parts) != 0 {
		configured++
	}
	if configured != 1 {
		return nil, errors.New("send_hex, send_text, parts 중 정확히 하나만 지정하세요. 반복 입력 예: {\"parts\":[{\"repeat_hex\":\"41\",\"count\":88}],\"append_newline\":true (이때 send_text/send_hex 생략)")
	}
	var payload []byte
	if a.SendHex != "" {
		var err error
		payload, err = hex.DecodeString(strings.Join(strings.Fields(a.SendHex), ""))
		if err != nil {
			return nil, fmt.Errorf("send_hex 해석: %w", err)
		}
	} else if a.SendText != "" {
		payload = []byte(a.SendText)
	} else {
		for _, part := range a.Parts {
			fields := 0
			if part.Text != "" {
				fields++
			}
			if part.Hex != "" {
				fields++
			}
			if part.RepeatHex != "" {
				fields++
			}
			if fields != 1 {
				return nil, errors.New("parts의 각 조각은 text, hex, repeat_hex 중 하나여야 합니다")
			}
			var chunk []byte
			if part.Text != "" {
				chunk = []byte(part.Text)
			}
			if part.Hex != "" {
				var err error
				chunk, err = hex.DecodeString(strings.Join(strings.Fields(part.Hex), ""))
				if err != nil {
					return nil, fmt.Errorf("parts.hex 해석: %w", err)
				}
			}
			if part.RepeatHex != "" {
				if part.Count < 1 || part.Count > 1024 {
					return nil, errors.New("parts.count는 1~1024여야 합니다")
				}
				var err error
				unit, decodeErr := hex.DecodeString(strings.Join(strings.Fields(part.RepeatHex), ""))
				err = decodeErr
				if err != nil {
					return nil, fmt.Errorf("parts.repeat_hex 해석: %w", err)
				}
				if len(unit) == 0 || len(unit)*part.Count > 1024 {
					return nil, errors.New("반복 조각은 최대 1024바이트여야 합니다")
				}
				for i := 0; i < part.Count; i++ {
					chunk = append(chunk, unit...)
				}
			}
			payload = append(payload, chunk...)
			if len(payload) > 1024 {
				return nil, errors.New("전송 데이터는 최대 1024바이트입니다")
			}
		}
	}
	if len(payload) == 0 || len(payload) > 1024 {
		return nil, errors.New("전송 데이터는 1~1024바이트여야 합니다")
	}
	if a.AppendNewline {
		if len(payload) == 1024 {
			return nil, errors.New("줄바꿈을 포함한 전송 데이터는 최대 1024바이트입니다")
		}
		payload = append(payload, '\n')
	}
	return payload, nil
}

func (t *ToolSet) closeTCPSessionLocked() {
	if t.tcpExpiry != nil {
		t.tcpExpiry.Stop()
		t.tcpExpiry = nil
	}
	if t.tcpConn != nil {
		_ = t.tcpConn.Close()
		t.tcpConn = nil
	}
}

func (t *ToolSet) resetTCPSessionExpiryLocked() {
	if t.tcpExpiry != nil {
		t.tcpExpiry.Stop()
	}
	t.tcpExpiry = time.AfterFunc(5*time.Minute, func() {
		t.tcpMu.Lock()
		defer t.tcpMu.Unlock()
		t.closeTCPSessionLocked()
	})
}
