package agent

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestExchangeTCPReadsBannerAndHex(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("Address of the buf: 0x1234\n"))
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	got, err := exchangeTCP(context.Background(), "127.0.0.1", port, "")
	if err != nil {
		t.Fatal(err)
	}
	if got["received_text"] != "Address of the buf: 0x1234\n" || got["received_hex"] != fmt.Sprintf("%x", "Address of the buf: 0x1234\n") {
		t.Fatalf("unexpected TCP response: %#v", got)
	}
}

func TestExchangeTCPWaitsForFragmentedPromptBeforeSending(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("Address of the buf: 0x1234\n"))
		time.Sleep(350 * time.Millisecond)
		_, _ = conn.Write([]byte("Distance between buf and $rbp: 96\nInput: "))
		buf := make([]byte, 8)
		n, _ := conn.Read(buf)
		if string(buf[:n]) == "1\n" {
			_, _ = conn.Write([]byte("CANARY-STAGE\n"))
		}
	}()
	got, err := exchangeTCP(context.Background(), "127.0.0.1", listener.Addr().(*net.TCPAddr).Port, "310a")
	if err != nil {
		t.Fatal(err)
	}
	if got["response_hex"] != fmt.Sprintf("%x", "CANARY-STAGE\n") {
		t.Fatalf("did not separate fragmented prompt from reply: %#v", got)
	}
}

func TestExchangeTCPReadsResponseAfterBannerOnSameConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("Address of the buf: 0x1234\n"))
		buf := make([]byte, 8)
		n, _ := conn.Read(buf)
		if string(buf[:n]) == "hello\n" {
			_, _ = conn.Write([]byte("ECHO:hello\n"))
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	got, err := exchangeTCP(context.Background(), "127.0.0.1", port, fmt.Sprintf("%x", "hello\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got["received_text"] != "Address of the buf: 0x1234\nECHO:hello\n" ||
		got["initial_hex"] != fmt.Sprintf("%x", "Address of the buf: 0x1234\n") ||
		got["response_hex"] != fmt.Sprintf("%x", "ECHO:hello\n") {
		t.Fatalf("missing post-input response: %#v", got)
	}
}

func TestExchangeTCPRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		host string
		port int
		hex  string
	}{
		{"", 80, ""}, {"host/path", 80, ""}, {"127.0.0.1", 0, ""}, {"127.0.0.1", 80, "zz"},
	} {
		if _, err := exchangeTCP(context.Background(), tc.host, tc.port, tc.hex); err == nil {
			t.Fatalf("accepted invalid input: %#v", tc)
		}
	}
}

func TestWorkerExposesTCPExchange(t *testing.T) {
	tools := NewToolSet(nil, "worker").WorkerTools()
	for _, tool := range tools {
		if tool.Name() == "tcp_exchange" {
			return
		}
	}
	t.Fatal("worker tool list does not include tcp_exchange")
}
