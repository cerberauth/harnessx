package harnessx

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

const (
	idDiscover    = "discover"
	idBoomVariant = "boom-variant"
	idSkipAll     = "skip-all"
	idBroken      = "broken-check"
	dupResourceID = "dup"
)

func globalCheck(id CheckID, deps []CheckID, run CheckFunc) Check {
	return Check{ID: id, Scope: ScopeGlobal, DependsOn: deps, Run: run}
}

func resourceCheck(id CheckID, deps []CheckID, run ResourceCheckFunc) Check {
	return Check{ID: id, Scope: ScopePerResource, DependsOn: deps, RunResource: run}
}

func discover(id CheckID, resources ...Resource) Check {
	return globalCheck(id, nil, func(context.Context, Target, ResultStore) (Result, error) {
		return Result{Resources: resources}, nil
	})
}

func runEngine(t *testing.T, checks ...Check) (ScanSummary, error) {
	t.Helper()
	e := New()
	if err := e.Register(checks...); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return e.Run(context.Background(), Target{})
}

func resultFor(t *testing.T, s ScanSummary, id CheckID) Result {
	t.Helper()
	for _, r := range s.Results {
		if r.CheckID == id {
			return r
		}
	}
	t.Fatalf("no result for check %q", id)
	return Result{}
}

func TestEngine_CheckReturnedSkipIsCountedAsSkipped(t *testing.T) {
	summary, err := runEngine(t,
		globalCheck("skips", nil, func(context.Context, Target, ResultStore) (Result, error) {
			return Skip("skips", "nothing to do"), nil
		}),
		globalCheck("runs", nil, func(context.Context, Target, ResultStore) (Result, error) {
			return Result{}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Skipped != 1 || summary.Executed != 1 || summary.Failed != 0 {
		t.Errorf("Executed/Skipped/Failed = %d/%d/%d, want 1/1/0", summary.Executed, summary.Skipped, summary.Failed)
	}
}

func TestEngine_PerResourceCheckHasCheckLevelResult(t *testing.T) {
	var sawPassed, sawObserved, sawSkipped atomic.Bool
	var observationsSeen atomic.Int64

	summary, err := runEngine(t,
		discover(idDiscover, Resource{ID: "a"}, Resource{ID: "b"}),
		resourceCheck("per-resource", []CheckID{idDiscover}, func(_ context.Context, _ Target, r Resource, _ ResultStore) (Result, error) {
			if r.ID == "b" {
				return Result{Observations: []Observation{{Title: "finding"}}}, nil
			}
			return Result{}, nil
		}),
		globalCheck("after", []CheckID{"per-resource"}, func(_ context.Context, _ Target, store ResultStore) (Result, error) {
			sawPassed.Store(IfCheckPassed("per-resource")(store))
			sawObserved.Store(IfCheckObserved("per-resource")(store))
			sawSkipped.Store(IfCheckSkipped("per-resource")(store))
			observationsSeen.Store(int64(len(store.Observations())))
			if _, ok := store.GetForResource("per-resource", "b"); !ok {
				t.Error("GetForResource lost the per-resource result")
			}
			return Result{}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if sawPassed.Load() {
		t.Error("IfCheckPassed = true, want false: one resource produced an observation")
	}
	if !sawObserved.Load() {
		t.Error("IfCheckObserved = false, want true for a per-resource dependency")
	}
	if sawSkipped.Load() {
		t.Error("IfCheckSkipped = true, want false")
	}
	if got := observationsSeen.Load(); got != 1 {
		t.Errorf("store.Observations() = %d, want 1: the aggregate must not double-count", got)
	}
	if summary.Executed != 4 { // discover + 2 resources + after
		t.Errorf("Executed = %d, want 4: the aggregate must not be counted in the summary", summary.Executed)
	}
}

func TestEngine_PerResourceAggregateCarriesErrorAndSkip(t *testing.T) {
	var failedGet, skippedGet Result
	_, err := runEngine(t,
		discover(idDiscover, Resource{ID: "a"}),
		resourceCheck("fails", []CheckID{idDiscover}, func(context.Context, Target, Resource, ResultStore) (Result, error) {
			return Result{}, errors.New("boom")
		}),
		Check{
			ID: idSkipAll, Scope: ScopePerResource, DependsOn: []CheckID{idDiscover},
			Skip:        SkipResourceWhen(func(context.Context, Target, Resource, ResultStore) string { return "nope" }),
			RunResource: func(context.Context, Target, Resource, ResultStore) (Result, error) { return Result{}, nil },
		},
		globalCheck("after", []CheckID{"fails", idSkipAll}, func(_ context.Context, _ Target, store ResultStore) (Result, error) {
			failedGet, _ = store.Get("fails")
			skippedGet, _ = store.Get(idSkipAll)
			return Result{}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if failedGet.Err == nil {
		t.Error("aggregate of a failing resource has no Err")
	}
	if !skippedGet.Skipped {
		t.Error("aggregate of an all-skipped check is not Skipped")
	}
}

func TestEngine_PanicErrorNamesCheckAndHasStack(t *testing.T) {
	summary, err := runEngine(t,
		globalCheck("boom", nil, func(context.Context, Target, ResultStore) (Result, error) { panic("kaboom") }),
		Check{
			ID: idBoomVariant, Scope: ScopeGlobal, Variants: []string{"v1"},
			RunVariant: func(context.Context, Target, string, ResultStore) (Result, error) { panic("variant kaboom") },
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var se *ScanError
	if r := resultFor(t, summary, "boom"); !errors.As(r.Err, &se) || se.CheckID != "boom" {
		t.Errorf("panic error = %v, want a ScanError for check %q", r.Err, "boom")
	} else if len(se.Stack) == 0 {
		t.Error("ScanError.Stack is empty for a recovered panic")
	}

	vr := resultFor(t, summary, idBoomVariant)
	var ve *ScanError
	if len(vr.Attempts) != 1 || !errors.As(vr.Attempts[0].Err, &ve) || ve.CheckID != idBoomVariant {
		t.Errorf("variant attempt error = %v, want a ScanError for check %q", vr.Attempts, idBoomVariant)
	}
	if !errors.As(vr.Err, &ve) || ve.CheckID != idBoomVariant {
		t.Errorf("variant result error = %v, want a ScanError for check %q", vr.Err, idBoomVariant)
	}
}

func TestScanError_MessageWithoutCheckID(t *testing.T) {
	got := (&ScanError{Cause: errors.New("x")}).Error()
	if want := "harnessx: check failed: x"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestEngine_RegisterIsAtomic(t *testing.T) {
	e := New()
	noop := func(context.Context, Target, ResultStore) (Result, error) { return Result{}, nil }
	if err := e.Register(globalCheck("a", nil, noop)); err != nil {
		t.Fatal(err)
	}

	err := e.Register(globalCheck("b", nil, noop), globalCheck("a", nil, noop))
	if !errors.Is(err, ErrDuplicateCheckID) {
		t.Fatalf("Register error = %v, want ErrDuplicateCheckID", err)
	}

	// "b" came before the duplicate, but the failed batch must not leave it behind.
	if err := e.Register(globalCheck("b", nil, noop)); err != nil {
		t.Errorf("registering b after a failed batch: %v", err)
	}

	err = e.Register(globalCheck("c", nil, noop), globalCheck("c", nil, noop))
	if !errors.Is(err, ErrDuplicateCheckID) {
		t.Errorf("duplicate inside one batch: error = %v, want ErrDuplicateCheckID", err)
	}
}

func TestEngine_NewDefersWithChecksErrorToRun(t *testing.T) {
	noop := func(context.Context, Target, ResultStore) (Result, error) { return Result{}, nil }
	e := New(WithChecks(globalCheck("a", nil, noop), globalCheck("a", nil, noop)))

	summary, err := e.Run(context.Background(), Target{})
	if !errors.Is(err, ErrDuplicateCheckID) {
		t.Fatalf("Run error = %v, want ErrDuplicateCheckID", err)
	}
	if summary.Executed != 0 {
		t.Errorf("Executed = %d, want nothing to run", summary.Executed)
	}
}

// requireInvalidCheck asserts that chk is rejected both when registered and,
// since a Scenario's checks bypass Register, when run through RunScenario.
func requireInvalidCheck(t *testing.T, chk Check) {
	t.Helper()
	e := New()
	if err := e.Register(chk); err == nil {
		t.Error("Register accepted an invalid check")
	}
	summary, err := e.RunScenario(context.Background(), Target{}, Scenario{Checks: []Check{chk}})
	if err == nil {
		t.Error("RunScenario ran an invalid check")
	}
	if summary.Executed+summary.Failed+summary.Skipped != 0 {
		t.Errorf("an invalid check produced results: %+v", summary)
	}
}

func TestEngine_RejectsInvalidScope(t *testing.T) {
	requireInvalidCheck(t, Check{ID: idBroken, Scope: CheckScope(7)})
}

func TestEngine_RejectsResourceSkipOnGlobalCheck(t *testing.T) {
	requireInvalidCheck(t, Check{
		ID: "g", Scope: ScopeGlobal,
		Skip: SkipResourceWhen(func(context.Context, Target, Resource, ResultStore) string { return "x" }),
		Run:  func(context.Context, Target, ResultStore) (Result, error) { return Result{}, nil },
	})
}

func TestEngine_RejectsMissingRunFunction(t *testing.T) {
	requireInvalidCheck(t, Check{ID: "no-run", Scope: ScopeGlobal})
	requireInvalidCheck(t, Check{ID: "no-run-resource", Scope: ScopePerResource})
	requireInvalidCheck(t, Check{ID: "no-run-variant", Scope: ScopeGlobal, Variants: []string{"a"}})
	requireInvalidCheck(t, Check{ID: "no-run-resource-variant", Scope: ScopePerResource, Variants: []string{"a"}})
}

func TestEngine_RegisterRejectsInvalidCheckAtomically(t *testing.T) {
	e := New()
	ok := globalCheck("ok", nil, func(context.Context, Target, ResultStore) (Result, error) { return Result{}, nil })
	if err := e.Register(ok, Check{ID: idBroken, Scope: ScopeGlobal}); err == nil {
		t.Fatal("Register accepted a check with no Run")
	}
	if err := e.Register(ok); err != nil {
		t.Errorf("a rejected batch left %q registered: %v", "ok", err)
	}
}

func TestEngine_NewDefersInvalidWithChecksToRun(t *testing.T) {
	e := New(WithChecks(Check{ID: idBroken, Scope: ScopeGlobal}))
	if _, err := e.Run(context.Background(), Target{}); err == nil {
		t.Error("Run ran despite an invalid WithChecks check")
	}
}

func TestEngine_SkipReasons(t *testing.T) {
	noop := func(context.Context, Target, ResultStore) (Result, error) { return Result{}, nil }
	summary, err := runEngine(t,
		Check{ID: "cond", Scope: ScopeGlobal, Run: noop, Conditions: []Condition{func(ResultStore) bool { return false }}},
		resourceCheck("orphan", nil, func(context.Context, Target, Resource, ResultStore) (Result, error) { return Result{}, nil }),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := resultFor(t, summary, "cond").SkipReason; got != "condition not met" {
		t.Errorf("condition skip reason = %q, want %q", got, "condition not met")
	}
	if got := resultFor(t, summary, "orphan").SkipReason; got != "no resources discovered" {
		t.Errorf("no-resources skip reason = %q, want %q", got, "no resources discovered")
	}
}

func TestEngine_DuplicateResourceIDsRunOnce(t *testing.T) {
	var runs atomic.Int64
	summary, err := runEngine(t,
		discover("d1", Resource{ID: dupResourceID, URL: "first"}, Resource{ID: "other"}),
		discover("d2", Resource{ID: dupResourceID, URL: "second"}),
		resourceCheck("each", []CheckID{"d1", "d2"}, func(context.Context, Target, Resource, ResultStore) (Result, error) {
			runs.Add(1)
			return Result{}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := runs.Load(); got != 2 {
		t.Errorf("per-resource check ran %d times, want 2 (one per distinct resource ID)", got)
	}
	if summary.Executed != 4 { // d1, d2 + 2 resources
		t.Errorf("Executed = %d, want 4", summary.Executed)
	}
}

func TestResult_Status(t *testing.T) {
	tests := []struct {
		name   string
		result Result
		want   ResultStatus
	}{
		{"passed", Result{}, StatusPassed},
		{"observations", Result{Observations: []Observation{{Title: "x"}}}, StatusFailed},
		{"is skipped", Result{Skipped: true}, StatusSkipped},
		{"error", Result{Err: errors.New("boom")}, StatusError},
		{"error beats skipped", Result{Skipped: true, Err: errors.New("boom")}, StatusError},
		{"error beats observations", Result{Err: errors.New("boom"), Observations: []Observation{{Title: "x"}}}, StatusError},
		{"skipped beats observations", Result{Skipped: true, Observations: []Observation{{Title: "x"}}}, StatusSkipped},
	}
	for _, tt := range tests {
		if got := tt.result.Status(); got != tt.want {
			t.Errorf("%s: Status() = %q, want %q", tt.name, got, tt.want)
		}
	}
}
