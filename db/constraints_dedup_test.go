package db

import (
	"sync"
	"testing"
)

func TestAddConstraintReusesEquivalentTextAcrossTurns(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer d.Close()
	expID, err := d.CreateExploration("constraint dedup", "constraint dedup")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	s := d.Exploration(expID)

	first, err := s.AddConstraint("deny", "외부 호스트 접근 금지", "goals")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{" 외부 호스트 접근 금지 ", "외부 호스트 접근 금지"} {
		id, err := s.AddConstraint("deny", text, "goals")
		if err != nil || id != first {
			t.Fatalf("duplicate id=%d err=%v, want %d", id, err, first)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	ids := make(chan int64, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := s.AddConstraint("deny", "외부 호스트 접근 금지", "goals")
			errs <- err
			ids <- id
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for id := range ids {
		if id != first {
			t.Fatalf("concurrent duplicate id=%d, want %d", id, first)
		}
	}
	constraints, err := s.ListConstraints()
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 1 {
		t.Fatalf("got %d stored constraints, want 1", len(constraints))
	}

	other, err := s.AddConstraint("allow", "외부 호스트 접근 금지", "human")
	if err != nil || other == first {
		t.Fatalf("different kind must remain distinct: id=%d err=%v", other, err)
	}
}
