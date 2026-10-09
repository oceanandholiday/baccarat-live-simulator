package model

import "testing"

func TestOutcomeValid(t *testing.T) {
	for _, o := range []Outcome{Player, Banker, Tie} {
		if !o.Valid() {
			t.Fatalf("%s should be valid", o)
		}
	}
	if Outcome("pair").Valid() {
		t.Fatal("unexpected outcome accepted")
	}
}

func TestStatsAddAndPercentages(t *testing.T) {
	var s Stats
	var err error
	for _, o := range []Outcome{Player, Player, Banker} {
		s, err = s.Add(o)
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.Total != 3 || s.Counts.Player != 2 || s.Counts.Banker != 1 || s.Counts.Tie != 0 {
		t.Fatalf("stats = %+v", s)
	}
	p := s.Percentages()
	if p.Player != 66.7 || p.Banker != 33.3 || p.Tie != 0 {
		t.Fatalf("percentages = %+v", p)
	}

	if _, err := s.Add(Outcome("void")); err == nil {
		t.Fatal("expected invalid outcome error")
	}
}

func TestZeroPercentages(t *testing.T) {
	p := (Stats{}).Percentages()
	if p.Player != 0 || p.Banker != 0 || p.Tie != 0 {
		t.Fatalf("percentages = %+v", p)
	}
}

func TestStatePrepareEmptyRounds(t *testing.T) {
	var s State
	s.Prepare()
	if s.Rounds == nil {
		t.Fatal("rounds should marshal as an empty array")
	}
	if s.Version != Version || s.HistoryLimit != DefaultHistory {
		t.Fatalf("state = %+v", s)
	}
}
