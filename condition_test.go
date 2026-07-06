package harnessx

import (
	"testing"
)

func TestIfCheckPassed(t *testing.T) {
	store := NewStaticResultStore(
		Result{CheckID: "ok", Observations: nil, Err: nil},
		Result{CheckID: "withObservations", Observations: []Observation{{Title: "something"}}},
		Result{CheckID: "skipped", Skipped: true},
	)

	if !IfCheckPassed("ok")(store) {
		t.Error("IfCheckPassed(ok) should be true")
	}
	if IfCheckPassed("withObservations")(store) {
		t.Error("IfCheckPassed(withObservations) should be false")
	}
	if IfCheckPassed("skipped")(store) {
		t.Error("IfCheckPassed(skipped) should be false")
	}
	if IfCheckPassed("missing")(store) {
		t.Error("IfCheckPassed(missing) should be false")
	}
}

func TestIfCheckObserved(t *testing.T) {
	store := NewStaticResultStore(
		Result{
			CheckID:      "withObs",
			Observations: []Observation{{Title: "issue found"}},
		},
		Result{
			CheckID:      "noObs",
			Observations: nil,
		},
	)

	if !IfCheckObserved("withObs")(store) {
		t.Error("IfCheckObserved(withObs) should be true")
	}
	if IfCheckObserved("noObs")(store) {
		t.Error("IfCheckObserved(noObs) should be false")
	}
	if IfCheckObserved("missing")(store) {
		t.Error("IfCheckObserved(missing) should be false")
	}
}

func TestIfCheckSkipped(t *testing.T) {
	store := NewStaticResultStore(
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
	store := NewStaticResultStore()
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
	store := NewStaticResultStore()
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
	store := NewStaticResultStore()
	alwaysTrue := func(ResultStore) bool { return true }
	alwaysFalse := func(ResultStore) bool { return false }

	if Not(alwaysTrue)(store) {
		t.Error("Not(true) should be false")
	}
	if !Not(alwaysFalse)(store) {
		t.Error("Not(false) should be true")
	}
}
