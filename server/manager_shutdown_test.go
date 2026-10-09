package server

import (
	"context"
	"testing"
)

func TestManagerCloseStopsServerBackgroundWorkers(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	s := New(context.Background(), m, t.TempDir(), t.TempDir(), t.TempDir())
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if s.ctx.Err() != context.Canceled {
		t.Fatalf("server context after manager close: %v", s.ctx.Err())
	}
	// Close has already waited for the archive and notifier loops. Repeated
	// waits are safe and would block here if either worker were still running.
	s.archiveWG.Wait()
	s.notifierWG.Wait()
}
