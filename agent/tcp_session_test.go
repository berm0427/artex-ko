package agent

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestTCPSessionOpenSendBeforeServerTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(1500 * time.Millisecond))
		_, _ = conn.Write([]byte("Input: "))
		buf := make([]byte, 8)
		n, err := conn.Read(buf)
		if err == nil && string(buf[:n]) == "A\n" {
			_, _ = conn.Write([]byte("[2] Next input: "))
		}
	}()
	set := NewToolSet(nil, "worker")
	defer func() { set.tcpMu.Lock(); set.closeTCPSessionLocked(); set.tcpMu.Unlock() }()
	out, err := set.runTCPSessionEx(context.Background(), tcpSessionArgs{
		Action: "open_send", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port,
		SendText: "A", AppendNewline: true,
	})
	if err != nil || out["initial_text"] != "Input: " || out["received_text"] != "[2] Next input: " {
		t.Fatalf("open_send: %v %#v", err, out)
	}
}

func TestTCPSessionOpenSequenceKeepsOneSocket(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = conn.Write([]byte("First: "))
		buf := make([]byte, 16)
		n, err := conn.Read(buf)
		if err != nil || string(buf[:n]) != "A\n" {
			return
		}
		_, _ = conn.Write([]byte("Second: "))
		n, err = conn.Read(buf)
		if err == nil && string(buf[:n]) == "B\n" {
			_, _ = conn.Write([]byte("Done\n"))
		}
	}()
	set := NewToolSet(nil, "worker")
	defer func() { set.tcpMu.Lock(); set.closeTCPSessionLocked(); set.tcpMu.Unlock() }()
	out, err := set.runTCPSessionEx(context.Background(), tcpSessionArgs{
		Action: "open_sequence", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port,
		Steps: []tcpSessionStep{{SendText: "A", AppendNewline: true}, {SendText: "B", AppendNewline: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	steps := out["steps"].([]map[string]any)
	if out["initial_text"] != "First: " || len(steps) != 2 || steps[0]["received_text"] != "Second: " || steps[1]["received_text"] != "Done\n" {
		t.Fatalf("unexpected sequence: %#v", out)
	}
}

func TestTCPSessionOpenSequenceNamesInvalidStep(t *testing.T) {
	set := NewToolSet(nil, "worker")
	_, err := set.runTCPSessionEx(context.Background(), tcpSessionArgs{
		Action: "open_sequence", Host: "127.0.0.1", Port: 9000,
		Steps: []tcpSessionStep{{SendText: "A"}, {AppendNewline: true}},
	})
	if err == nil || !strings.Contains(err.Error(), "steps[1] (단계 2)") {
		t.Fatalf("expected actionable second-step error, got %v", err)
	}
}

func TestTCPSequenceToolRunsTwoSteps(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("tcp sequence tool test", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = conn.Write([]byte("First: "))
		buf := make([]byte, 16)
		n, _ := conn.Read(buf)
		if string(buf[:n]) != "A\n" {
			return
		}
		_, _ = conn.Write([]byte("Second: "))
		n, _ = conn.Read(buf)
		if string(buf[:n]) == "B\n" {
			_, _ = conn.Write([]byte("Done\n"))
		}
	}()
	set := NewToolSet(d.Exploration(task.ExplorationID), "worker")
	set.SetTaskID(task.ID)
	defer func() { set.tcpMu.Lock(); set.closeTCPSessionLocked(); set.tcpMu.Unlock() }()
	input := fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"steps":[{"send_text":"A","append_newline":true},{"parts":[{"repeat_hex":"42","count":1}],"append_newline":true}]}`, listener.Addr().(*net.TCPAddr).Port)
	result, err := set.tcpSequence().Call(context.Background(), []byte(input), nil)
	if err != nil || !strings.Contains(result.Flatten(), "Done") {
		t.Fatalf("tcp_sequence tool: %v %q", err, result.Flatten())
	}
}

func TestTCPSessionKeepsConnectionAcrossInputs(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("Choice: "))
		buf := make([]byte, 16)
		n, _ := conn.Read(buf)
		if string(buf[:n]) != "1\n" {
			return
		}
		_, _ = conn.Write([]byte("Input: "))
		n, _ = conn.Read(buf)
		if string(buf[:n]) == "hello\n" {
			_, _ = conn.Write([]byte("Echo: hello\n"))
		}
	}()
	set := NewToolSet(nil, "worker")
	defer func() { set.tcpMu.Lock(); set.closeTCPSessionLocked(); set.tcpMu.Unlock() }()
	ctx := context.Background()
	port := listener.Addr().(*net.TCPAddr).Port
	opened, err := set.runTCPSession(ctx, "open", "127.0.0.1", port, "")
	if err != nil || opened["received_text"] != "Choice: " {
		t.Fatalf("open: %v %#v", err, opened)
	}
	choice, err := set.runTCPSession(ctx, "send", "", 0, "310a")
	if err != nil || choice["received_text"] != "Input: " {
		t.Fatalf("choice: %v %#v", err, choice)
	}
	answer, err := set.runTCPSession(ctx, "send", "", 0, fmt.Sprintf("%x", "hello\n"))
	if err != nil || answer["received_text"] != "Echo: hello\n" {
		t.Fatalf("answer: %v %#v", err, answer)
	}
	if _, err := set.runTCPSession(ctx, "close", "", 0, ""); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerExposesTCPSession(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range NewToolSet(nil, "worker").WorkerTools() {
		seen[tool.Name()] = true
	}
	if !seen["tcp_session"] || !seen["tcp_sequence"] || !seen["tcp_repeat_sequence"] {
		t.Fatalf("worker tool list missing TCP tools: %#v", seen)
	}
}

func TestTCPRepeatSequenceReportsExactSentBytes(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("tcp repeat sequence tool test", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = conn.Write([]byte("Input: "))
		buf := make([]byte, 16)
		n, _ := conn.Read(buf)
		if string(buf[:n]) == "AAAA\n" {
			_, _ = conn.Write([]byte("Next: "))
		}
	}()
	set := NewToolSet(d.Exploration(task.ExplorationID), "worker")
	set.SetTaskID(task.ID)
	defer func() { set.tcpMu.Lock(); set.closeTCPSessionLocked(); set.tcpMu.Unlock() }()
	input := fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"steps":[{"byte_hex":"41","count":4,"append_newline":true}]}`, listener.Addr().(*net.TCPAddr).Port)
	result, err := set.tcpRepeatSequence().Call(context.Background(), []byte(input), nil)
	if err != nil || !strings.Contains(result.Flatten(), `"sent_bytes":5`) || !strings.Contains(result.Flatten(), "Next: ") {
		t.Fatalf("tcp_repeat_sequence: %v %q", err, result.Flatten())
	}
}

func TestTCPSessionEmptyReplyDoesNotCloseConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("Input: "))
		buf := make([]byte, 16)
		_, _ = conn.Read(buf)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("second input received\n"))
	}()
	set := NewToolSet(nil, "worker")
	defer func() { set.tcpMu.Lock(); set.closeTCPSessionLocked(); set.tcpMu.Unlock() }()
	ctx := context.Background()
	if _, err := set.runTCPSession(ctx, "open", "127.0.0.1", listener.Addr().(*net.TCPAddr).Port, ""); err != nil {
		t.Fatal(err)
	}
	first, err := set.runTCPSession(ctx, "send", "", 0, "310a")
	if err != nil || first["received_bytes"] != 0 {
		t.Fatalf("first: %v %#v", err, first)
	}
	second, err := set.runTCPSession(ctx, "send", "", 0, "320a")
	if err != nil || second["received_text"] != "second input received\n" {
		t.Fatalf("second: %v %#v", err, second)
	}
}

// Explicit opt-in only: exercises a user-authorized raw TCP lab endpoint.
func TestTCPSessionRemoteLab(t *testing.T) {
	host := os.Getenv("ARTEX_REMOTE_LAB_HOST")
	portText := os.Getenv("ARTEX_REMOTE_LAB_PORT")
	if host == "" || portText == "" {
		t.Skip("remote lab endpoint not configured")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	set := NewToolSet(nil, "worker")
	defer func() { set.tcpMu.Lock(); set.closeTCPSessionLocked(); set.tcpMu.Unlock() }()
	ctx := context.Background()
	opened, err := set.runTCPSessionEx(ctx, tcpSessionArgs{Action: "open_send", Host: host, Port: port, SendText: "A", AppendNewline: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(opened["initial_text"].(string), "Input:") {
		t.Fatalf("incomplete prompt: %#v", opened)
	}
	if !strings.Contains(opened["received_text"].(string), "[2] Overwrite the return address") {
		t.Fatalf("missing stage-two prompt: %#v", opened)
	}
}

func TestTCPSessionPayloadParts(t *testing.T) {
	payload, err := tcpSessionPayload(tcpSessionArgs{Parts: []tcpSessionPart{{RepeatHex: "41", Count: 88}, {Hex: "0a"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != 89 || payload[0] != 'A' || payload[87] != 'A' || payload[88] != '\n' {
		t.Fatalf("unexpected payload: %x", payload)
	}
	textPayload, err := tcpSessionPayload(tcpSessionArgs{SendText: "hello\n"})
	if err != nil || string(textPayload) != "hello\n" {
		t.Fatalf("text payload: %v %q", err, textPayload)
	}
	if _, err := tcpSessionPayload(tcpSessionArgs{SendText: "x", SendHex: "78"}); err == nil {
		t.Fatal("accepted conflicting formats")
	}
	line, err := tcpSessionPayload(tcpSessionArgs{SendText: "A", AppendNewline: true})
	if err != nil || string(line) != "A\n" {
		t.Fatalf("newline payload: %v %q", err, line)
	}
}
