package harnessx

import (
	"testing"
)

// staticStore is a minimal ResultStore for condition testing.
type staticStore struct {
	results map[CheckID]Result
}

func newStaticStore(results ...Result) *staticStore {
	s := &staticStore{results: make(map[CheckID]Result)}
	for _, r := range results {
		s.results[r.CheckID] = r
	}
	return s
}

func (s *staticStore) Get(id CheckID) (Result, bool) {
	r, ok := s.results[id]
	return r, ok
}

func (s *staticStore) GetForResource(id CheckID, _ string) (Result, bool) {
	r, ok := s.results[id]
	return r, ok
}

func (s *staticStore) FindingsBySeverity(min Severity) []Finding {
	minRank := severityRank[min]
	var out []Finding
	for _, r := range s.results {
		for _, f := range r.Findings {
			if severityRank[f.Severity] >= minRank {
				out = append(out, f)
			}
		}
	}
	return out
}

func (s *staticStore) Resources() []Resource { return nil }

func TestIfCheckPassed(t *testing.T) {
	store := newStaticStore(
		Result{CheckID: "ok", Findings: nil, Err: nil},
		Result{CheckID: "withFindings", Findings: []Finding{{Severity: SeverityLow}}},
		Result{CheckID: "skipped", Skipped: true},
	)

	if !IfCheckPassed("ok")(store) {
		t.Error("IfCheckPassed(ok) should be true")
	}
	if IfCheckPassed("withFindings")(store) {
		t.Error("IfCheckPassed(withFindings) should be false")
	}
	if IfCheckPassed("skipped")(store) {
		t.Error("IfCheckPassed(skipped) should be false")
	}
	if IfCheckPassed("missing")(store) {
		t.Error("IfCheckPassed(missing) should be false")
	}
}

func TestIfCheckFound(t *testing.T) {
	store := newStaticStore(
		Result{
			CheckID: "withHigh",
			Findings: []Finding{
				{Severity: SeverityHigh},
			},
		},
		Result{
			CheckID:  "noFindings",
			Findings: nil,
		},
	)

	if !IfCheckFound("withHigh", SeverityMedium)(store) {
		t.Error("IfCheckFound(withHigh, medium) should be true")
	}
	if !IfCheckFound("withHigh", SeverityHigh)(store) {
		t.Error("IfCheckFound(withHigh, high) should be true")
	}
	if IfCheckFound("withHigh", SeverityCritical)(store) {
		t.Error("IfCheckFound(withHigh, critical) should be false")
	}
	if IfCheckFound("noFindings", SeverityInfo)(store) {
		t.Error("IfCheckFound(noFindings, info) should be false")
	}
	if IfCheckFound("missing", SeverityInfo)(store) {
		t.Error("IfCheckFound(missing, info) should be false")
	}
}

func TestIfCheckSkipped(t *testing.T) {
	store := newStaticStore(
		Result{CheckID: "skipped", Skipped: true},
		Result{CheckID: "ran"},
	)

	if !IfCheckSkipped("skipped")(store) {
		t.Error("IfCheckSkipped(skipped) should be true")
	}
	if IfCheckSkipped("ran")(store) {
		t.Error("IfCheckSkipped(ran) should be false")
	}
	if IfCheckSkipped("missing")(store) {
		t.Error("IfCheckSkipped(missing) should be false")
	}
}

func TestAll(t *testing.T) {
	store := newStaticStore()
	alwaysTrue := func(ResultStore) bool { return true }
	alwaysFalse := func(ResultStore) bool { return false }

	if !All(alwaysTrue, alwaysTrue)(store) {
		t.Error("All(true, true) should be true")
	}
	if All(alwaysTrue, alwaysFalse)(store) {
		t.Error("All(true, false) should be false")
	}
	if !All()(store) {
		t.Error("All() with no args should be true")
	}
}

func TestAny(t *testing.T) {
	store := newStaticStore()
	alwaysTrue := func(ResultStore) bool { return true }
	alwaysFalse := func(ResultStore) bool { return false }

	if !Any(alwaysFalse, alwaysTrue)(store) {
		t.Error("Any(false, true) should be true")
	}
	if Any(alwaysFalse, alwaysFalse)(store) {
		t.Error("Any(false, false) should be false")
	}
	if Any()(store) {
		t.Error("Any() with no args should be false")
	}
}

func TestNot(t *testing.T) {
	store := newStaticStore()
	alwaysTrue := func(ResultStore) bool { return true }
	alwaysFalse := func(ResultStore) bool { return false }

	if Not(alwaysTrue)(store) {
		t.Error("Not(true) should be false")
	}
	if !Not(alwaysFalse)(store) {
		t.Error("Not(false) should be true")
	}
}
