package db

import "testing"

func TestSuccessfulToolQuoteMatchesDecodedHTTPBody(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer d.Close()
	expID, err := d.CreateExploration("quote body", "quote body")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	s := d.Exploration(expID)
	intentID, err := s.AddIntent(map[string]any{"summary": "read invoice"}, 1, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := s.AddIntent(map[string]any{"summary": "other"}, 1, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	const body = `{"id":"INV-1002","owner":"bob","marker":"BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB"}`
	const wrapped = `{"body":"{\"id\":\"INV-1002\",\"owner\":\"bob\",\"marker\":\"BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB\"}","status":200}`
	stepID, err := s.AppendActivity(Activity{NodeID: &intentID, Kind: "tool_result", Tool: "lab_invoice", Detail: wrapped})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.SuccessfulToolQuote(intentID, body)
	if err != nil || got != stepID {
		t.Fatalf("decoded body match=%d err=%v, want %d", got, err, stepID)
	}
	got, err = s.SuccessfulToolQuote(intentID, `{"id": "INV-1002", "owner": "bob", "marker": "BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB"}`)
	if err != nil || got != stepID {
		t.Fatalf("equivalent JSON body match=%d err=%v, want %d", got, err, stepID)
	}
	got, err = s.SuccessfulToolQuote(intentID, `{"body":{"id":"INV-1002","owner":"bob","marker":"BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB"},"status":200}`)
	if err != nil || got != stepID {
		t.Fatalf("equivalent HTTP wrapper match=%d err=%v, want %d", got, err, stepID)
	}
	for _, changed := range []string{
		`{"body":{"id":"INV-1002","owner":"alice","marker":"BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB"},"status":200}`,
		`{"body":{"id":"INV-1002","owner":"bob","marker":"BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB"},"status":403}`,
	} {
		got, err = s.SuccessfulToolQuote(intentID, changed)
		if err != nil || got != 0 {
			t.Fatalf("altered HTTP wrapper accepted: %d %v", got, err)
		}
	}
	for _, id := range []int64{otherID, 0} {
		got, err := s.SuccessfulToolQuote(id, body)
		if err != nil || got != 0 {
			t.Fatalf("wrong intent %d accepted quote: %d %v", id, got, err)
		}
	}
	got, err = s.SuccessfulToolQuote(intentID, `{"id":"INV-9999","owner":"bob"}`)
	if err != nil || got != 0 {
		t.Fatalf("invented body accepted: %d %v", got, err)
	}
	got, err = s.SuccessfulToolQuote(intentID, `{"id":"INV-1002","owner":"alice","marker":"BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB"}`)
	if err != nil || got != 0 {
		t.Fatalf("altered owner accepted: %d %v", got, err)
	}
}
