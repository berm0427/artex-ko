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
	// A task loop can still use PostgreSQL while unwinding after cancellation.
	// Close must wait for it before closing the database.
	finished := make(chan error, 1)
	rt := s.engine.registerTaskRoutines(s.ctx, "shutdown-test", 1)
	runTaskRoutine(rt, func(ctx context.Context) {
		<-ctx.Done()
		_, err := m.pg.ListTools()
		finished <- err
	})
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatalf("task loop accessed a closed database: %v", err)
	}
	if s.ctx.Err() != context.Canceled {
		t.Fatalf("server context after manager close: %v", s.ctx.Err())
	}
	// Close has already waited for the archive and notifier loops. Repeated
	// waits are safe and would block here if either worker were still running.
	s.archiveWG.Wait()
	s.notifierWG.Wait()
	s.engine.waitShutdown()
}
