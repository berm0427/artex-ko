package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	actool "github.com/Autumn-27/norma/tool"
)

// tcpExchange gives workers a bounded, one-shot raw TCP observation without
// relying on the host shell's dialect or an optional netcat installation.
func (t *ToolSet) tcpExchange() actool.CoreTool {
	return t.readExpTool("tcp_exchange", "허가된 대상의 원시 TCP 서비스에 한 번 연결해 선택적 바이트를 보내고 응답 원문을 읽습니다. HTTP 전용 도구나 셸 스크립트가 아닙니다. 대상 범위를 벗어나지 마세요. send_hex는 선택 사항이며 비우면 먼저 오는 배너만 읽습니다. 응답은 UTF-8 텍스트와 hex를 함께 반환합니다.",
		obj(map[string]any{
			"host":     str("허가된 대상의 호스트명 또는 IP"),
			"port":     intp("허가된 대상의 TCP 포트"),
			"send_hex": str("선택: 전송할 바이트의 16진수 문자열. 서버가 먼저 배너를 보내면 같은 연결에서 이를 읽은 뒤 바이트를 보내고 후속 응답도 읽습니다."),
		}, "host", "port"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Host    string `json:"host"`
				Port    int    `json:"port"`
				SendHex string `json:"send_hex"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(fmt.Sprintf("잘못된 인자: %v", err)), nil
			}
			out, err := exchangeTCP(ctx, a.Host, a.Port, a.SendHex)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("TCP 관찰 실패: %v", err)), nil
			}
			return jsonResult(out)
		})
}

func exchangeTCP(ctx context.Context, host string, port int, sendHex string) (map[string]any, error) {
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "/\\\r\n\t ") || port < 1 || port > 65535 {
		return nil, errors.New("유효한 호스트와 TCP 포트가 필요합니다")
	}
	sendHex = strings.Join(strings.Fields(sendHex), "")
	if len(sendHex) > 2048 {
		return nil, errors.New("전송 데이터는 최대 1024바이트입니다")
	}
	payload, err := hex.DecodeString(sendHex)
	if err != nil {
		return nil, fmt.Errorf("send_hex 해석: %w", err)
	}
	conn, err := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	_ = conn.SetDeadline(deadline)
	var initial []byte
	if len(payload) > 0 {
		// A greeting can arrive in several packets. Wait for a brief quiet period
		// before writing so the following read is the response to our input.
		initial, err = readTCPBurst(conn, deadline, 750*time.Millisecond)
		if err != nil {
			return nil, err
		}
		if _, err := conn.Write(payload); err != nil {
			return nil, err
		}
	}
	response, err := readTCPBurst(conn, deadline, time.Until(deadline))
	if err != nil {
		return nil, err
	}
	data := append(append([]byte(nil), initial...), response...)
	text := string(data)
	if !utf8.Valid(data) {
		text = strings.ToValidUTF8(text, "�")
	}
	return map[string]any{
		"host": host, "port": port, "sent_hex": hex.EncodeToString(payload),
		"received_bytes": len(data), "received_hex": hex.EncodeToString(data), "received_text": text,
		"initial_hex": hex.EncodeToString(initial), "response_hex": hex.EncodeToString(response),
	}, nil
}

// readTCPBurst collects fragmented output until a short quiet period, EOF, or
// the exchange deadline. A timeout is not a protocol error: prompts commonly
// leave the connection open while waiting for the next line of input.
func readTCPBurst(conn net.Conn, deadline time.Time, firstWait time.Duration) ([]byte, error) {
	result, err := readTCPBurstState(conn, deadline, firstWait)
	return result.Data, err
}

type tcpReadState struct {
	Data       []byte
	PeerClosed bool
	TimedOut   bool
}

func readTCPBurstState(conn net.Conn, deadline time.Time, firstWait time.Duration) (tcpReadState, error) {
	var state tcpReadState
	var data []byte
	for len(data) < 8192 {
		wait := 600 * time.Millisecond
		if len(data) == 0 {
			wait = firstWait
		}
		if wait <= 0 || time.Now().Add(wait).After(deadline) {
			wait = time.Until(deadline)
		}
		if wait <= 0 {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(wait))
		buf := make([]byte, 8192-len(data))
		n, err := conn.Read(buf)
		data = append(data, buf[:n]...)
		if err == io.EOF {
			state.PeerClosed = true
			break
		}
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				state.TimedOut = true
				break
			}
			return state, err
		}
	}
	state.Data = data
	return state, nil
}
