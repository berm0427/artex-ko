package agent

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

type streamingOnlyCompactionProvider struct {
	streamCalls   int
	completeCalls int
	streamErr     error
}

func (p *streamingOnlyCompactionProvider) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.streamCalls++
	return func(yield func(llm.StreamEvent, error) bool) {
		if req.Thinking != "disabled" || req.MaxTokens != 1500 {
			yield(llm.StreamEvent{}, errors.New("unexpected compaction request"))
			return
		}
		if !yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "compressed "}, nil) {
			return
		}
		if p.streamErr != nil {
			yield(llm.StreamEvent{}, p.streamErr)
			return
		}
		yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "body"}, nil)
	}
}

func (p *streamingOnlyCompactionProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.completeCalls++
	return llm.Message{}, "", llm.Usage{}, errors.New("non-streaming request rejected")
}

func TestCompactionUsesStreamingProvider(t *testing.T) {
	p := &streamingOnlyCompactionProvider{}
	compactor := NewCompactor(p, "test")
	got, err := compactor.compress(context.Background(), &coldGraph{}, block{}, nil)
	if err != nil || got != "compressed body" {
		t.Fatalf("compress = %q, %v", got, err)
	}
	if p.streamCalls != 1 || p.completeCalls != 0 {
		t.Fatalf("stream calls = %d, complete calls = %d", p.streamCalls, p.completeCalls)
	}
}

func TestCompactionDoesNotPersistPartialStream(t *testing.T) {
	p := &streamingOnlyCompactionProvider{streamErr: errors.New("stream interrupted")}
	compactor := NewCompactor(p, "test")
	got, err := compactor.compress(context.Background(), &coldGraph{}, block{}, nil)
	if got != "" || err == nil || !strings.Contains(err.Error(), "stream interrupted") {
		t.Fatalf("compress = %q, %v", got, err)
	}
}
