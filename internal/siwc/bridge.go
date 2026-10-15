package siwc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Bridge struct {
	Store   *Store
	Client  *http.Client
	APIBase string // tests only; production uses the public OpenAI Responses API
}

func (b *Bridge) client() *http.Client {
	if b.Client != nil {
		return b.Client
	}
	return &http.Client{Timeout: 20 * time.Minute}
}

func (b *Bridge) base() string {
	if b.APIBase != "" {
		return strings.TrimRight(b.APIBase, "/")
	}
	return resource
}

func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/responses" && r.URL.Path != "/v1/models" {
		http.NotFound(w, r)
		return
	}
	s := b.Store.Snapshot()
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, "Bearer ")), []byte(s.BridgeKey)) != 1 {
		http.Error(w, "invalid local bridge key", http.StatusUnauthorized)
		return
	}
	token, err := b.Store.Access(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		b.models(w, r, token)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
		b.responses(w, r, token)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (b *Bridge) upstream(ctx context.Context, method, path, token string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, b.base()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if path == "/responses" {
		req.Header.Set("Accept", "text/event-stream")
	}
	return b.client().Do(req)
}

func copyError(w http.ResponseWriter, resp *http.Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}

func (b *Bridge) models(w http.ResponseWriter, r *http.Request, token string) {
	resp, err := b.upstream(r.Context(), http.MethodGet, "/models", token, nil)
	if err != nil {
		http.Error(w, "model catalog request failed", 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		copyError(w, resp)
		return
	}
	var catalog struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&catalog); err != nil {
		http.Error(w, "invalid model catalog", 502)
		return
	}
	var data []map[string]string
	for _, m := range catalog.Models {
		if m.Visibility == "list" && m.Slug != "" {
			data = append(data, map[string]string{"id": m.Slug})
		}
	}
	if data == nil {
		data = []map[string]string{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

var unsupportedFields = []string{
	"background", "conversation", "max_output_tokens", "max_tool_calls", "metadata", "moderation",
	"multi_agent", "prompt", "prompt_cache_retention", "safety_identifier", "temperature",
	"top_logprobs", "top_p", "truncation", "user", "previous_response_id",
}

func normalizeRequest(raw []byte) ([]byte, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if string(body["stream"]) == "false" {
		return nil, errors.New("ChatGPT plan requires streaming; enable streaming in the ARTEX LLM profile")
	}
	if len(body["model"]) == 0 || len(body["input"]) == 0 {
		return nil, errors.New("model and input are required")
	}
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(body["input"], &input); err != nil {
		return nil, errors.New("input must be an array")
	}
	for _, item := range input {
		var role, kind string
		_ = json.Unmarshal(item["role"], &role)
		_ = json.Unmarshal(item["type"], &kind)
		if role == "system" {
			return nil, errors.New("system input item is not supported; use instructions")
		}
		if kind == "function_call" || kind == "function_call_output" {
			item["namespace"] = json.RawMessage(`"artex"`)
		}
	}
	if updated, err := json.Marshal(input); err == nil {
		body["input"] = updated
	} else {
		return nil, err
	}
	if rawTools := body["tools"]; len(rawTools) != 0 {
		var tools []json.RawMessage
		if err := json.Unmarshal(rawTools, &tools); err != nil {
			return nil, errors.New("tools must be an array")
		}
		if len(tools) > 0 {
			group, err := json.Marshal([]map[string]any{{"type": "namespace", "name": "artex", "description": "ARTEX local analysis and execution tools", "tools": tools}})
			if err != nil {
				return nil, err
			}
			body["tools"] = group
		}
	}
	for _, name := range unsupportedFields {
		delete(body, name)
	}
	body["stream"] = json.RawMessage("true")
	body["store"] = json.RawMessage("false")
	return json.Marshal(body)
}

func (b *Bridge) responses(w http.ResponseWriter, r *http.Request, token string) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "request read failed", 400)
		return
	}
	clean, err := normalizeRequest(raw)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	resp, err := b.upstream(r.Context(), http.MethodPost, "/responses", token, bytes.NewReader(clean))
	if err != nil {
		http.Error(w, "OpenAI inference request failed", 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		copyError(w, resp)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	completed, failed := false, false
	var frame []string
	forward := func() bool {
		if len(frame) == 0 {
			return true
		}
		kind := ""
		for _, line := range frame {
			if strings.HasPrefix(line, "event: ") {
				kind = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			}
			if strings.HasPrefix(line, "data: ") {
				var f struct {
					Type string `json:"type"`
				}
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f) == nil && f.Type != "" {
					kind = f.Type
				}
			}
		}
		if kind == "response.incomplete" {
			failed = true
			encoded, _ := json.Marshal(map[string]any{"type": "response.failed", "error": map[string]string{"message": "OpenAI response was incomplete"}})
			_, err := fmt.Fprintf(w, "event: response.failed\ndata: %s\n\n", encoded)
			if flusher != nil {
				flusher.Flush()
			}
			return err == nil
		}
		if kind == "response.completed" {
			completed = true
		}
		if kind == "response.failed" || kind == "error" {
			failed = true
		}
		for _, line := range frame {
			if _, err := fmt.Fprintln(w, line); err != nil {
				return false
			}
		}
		_, err := fmt.Fprintln(w)
		if flusher != nil {
			flusher.Flush()
		}
		return err == nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			frame = append(frame, line)
			continue
		}
		if !forward() {
			return
		}
		frame = frame[:0]
	}
	if len(frame) > 0 && !forward() {
		return
	}
	if !completed && !failed {
		msg := "OpenAI response stream ended before response.completed"
		if scanner.Err() != nil {
			msg = "OpenAI response stream interrupted"
		}
		encoded, _ := json.Marshal(map[string]any{"type": "response.failed", "error": map[string]string{"message": msg}})
		_, _ = fmt.Fprintf(w, "event: response.failed\ndata: %s\n\n", encoded)
		if flusher != nil {
			flusher.Flush()
		}
	}
}
