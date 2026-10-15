package siwc

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *Session) error {
		s.AccessToken = "test-access"
		s.Scopes = "resource.invoke chatgpt.tokens.use.direct"
		s.ExpiresAt = time.Now().Add(time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestStoreRoundTrip(t *testing.T) {
	st := testStore(t)
	second, err := OpenStore(filepath.Dir(st.path))
	if err != nil {
		t.Fatal(err)
	}
	if second.Snapshot().AccessToken != "test-access" || second.Snapshot().HostID != st.Snapshot().HostID {
		t.Fatal("encrypted credential record did not round-trip")
	}
}

func TestNormalizeRequest(t *testing.T) {
	clean, err := normalizeRequest([]byte(`{"model":"gpt-test","input":[{"role":"user","content":"hello"}],"tools":[{"type":"function","name":"Bash"}],"max_output_tokens":1024,"temperature":0.5,"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(clean, &m); err != nil {
		t.Fatal(err)
	}
	if m["stream"] != true || m["store"] != false || m["max_output_tokens"] != nil || m["temperature"] != nil {
		t.Fatalf("bad SIWC request: %s", clean)
	}
	if _, ok := m["tools"]; !ok {
		t.Fatal("ARTEX tools were lost")
	}
	tools := m["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "artex" {
		t.Fatal("ARTEX tools were not namespaced")
	}
	if _, err := normalizeRequest([]byte(`{"model":"x","input":[{"role":"user"}],"stream":false}`)); err == nil {
		t.Fatal("nonstreaming accepted")
	}
	if _, err := normalizeRequest([]byte(`{"model":"x","input":[{"role":"system"}]}`)); err == nil {
		t.Fatal("system message accepted")
	}
}

func TestBridgeModelsAndStreaming(t *testing.T) {
	store := testStore(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-access" {
			t.Errorf("upstream token missing")
			return
		}
		switch r.URL.Path {
		case "/models":
			_, _ = io.WriteString(w, `{"models":[{"slug":"gpt-ok","visibility":"list"},{"slug":"hidden","visibility":"hidden"}]}`)
		case "/responses":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["store"] != false || body["stream"] != true || body["max_output_tokens"] != nil {
				t.Errorf("bad body: %#v", body)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
		default:
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	bridge := &Bridge{Store: store, APIBase: upstream.URL}
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/v1/models", ""},
		{"POST", "/v1/responses", `{"model":"gpt-ok","input":[{"role":"user","content":"hi"}],"max_output_tokens":100,"stream":true}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		req.Header.Set("Authorization", "Bearer "+store.Snapshot().BridgeKey)
		w := httptest.NewRecorder()
		bridge.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("%s: status=%d body=%s", tc.path, w.Code, w.Body.String())
		}
		if tc.path == "/v1/models" && (!strings.Contains(w.Body.String(), "gpt-ok") || strings.Contains(w.Body.String(), "hidden")) {
			t.Fatal(w.Body.String())
		}
		if tc.path == "/v1/responses" && !strings.Contains(w.Body.String(), "response.completed") {
			t.Fatal(w.Body.String())
		}
	}
	bad := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	bridge.ServeHTTP(w, bad)
	if w.Code != 401 {
		t.Fatalf("unauthenticated bridge status=%d", w.Code)
	}
}

func TestIncompleteStreamIsFailure(t *testing.T) {
	store := testStore(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.incomplete\ndata: {\"type\":\"response.incomplete\"}\n\n")
	}))
	defer upstream.Close()
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test","input":[{"role":"user","content":"hi"}],"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+store.Snapshot().BridgeKey)
	w := httptest.NewRecorder()
	(&Bridge{Store: store, APIBase: upstream.URL}).ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), "event: response.incomplete") || !strings.Contains(w.Body.String(), "event: response.failed") {
		t.Fatalf("incomplete was not converted to failure: %s", w.Body.String())
	}
}
