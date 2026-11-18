package harnessx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInternalChecksHiddenFromReportersAndSummary(t *testing.T) {
	rep := &testReporter{}
	e := New(WithReporters(rep), WithChecks(
		Check{ID: "fetch", Internal: true, Scope: ScopeGlobal, Run: func(context.Context, Target, ResultStore) (Result, error) {
			return Result{Data: "body", Resources: []Resource{{ID: "r1"}}}, nil
		}},
		Check{ID: "ctx", Internal: true, Scope: ScopePerResource, DependsOn: []CheckID{"fetch"}, RunResource: func(_ context.Context, _ Target, res Resource, _ ResultStore) (Result, error) {
			return Result{Data: res.ID}, nil
		}},
		Check{ID: testCheckIDProbe, Scope: ScopePerResource, DependsOn: []CheckID{"ctx"}, RunResource: func(_ context.Context, _ Target, res Resource, store ResultStore) (Result, error) {
			body, _ := GetData[string](store, "fetch")
			ctx, ok := store.GetForResource("ctx", res.ID)
			if !ok {
				return Result{}, errors.New("internal result not readable")
			}
			return Result{Observations: []Observation{{Title: body + "/" + ctx.Data.(string)}}}, nil
		}},
	))

	summary, err := e.Run(context.Background(), Target{})
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalChecks != 1 || rep.totalChecks != 1 {
		t.Errorf("TotalChecks = %d, reporter = %d, want 1", summary.TotalChecks, rep.totalChecks)
	}
	if summary.Executed != 1 || len(summary.Results) != 1 || summary.Results[0].CheckID != testCheckIDProbe {
		t.Errorf("summary = %+v", summary)
	}
	if len(summary.Observations) != 1 || summary.Observations[0].Title != "body/r1" {
		t.Errorf("observations = %+v", summary.Observations)
	}
	for _, id := range rep.starts {
		if id != testCheckIDProbe {
			t.Errorf("OnCheckStart for internal check %q", id)
		}
	}
	for _, r := range rep.completes {
		if r.CheckID != testCheckIDProbe {
			t.Errorf("OnCheckComplete for internal check %q", r.CheckID)
		}
	}
}

func TestDuplicateIDErrorNamesBothChecks(t *testing.T) {
	run := func(context.Context, Target, ResultStore) (Result, error) { return Result{}, nil }
	err := New().Register(
		Check{ID: "pack.baseline", Name: "JWT Baseline", Run: run},
		Check{ID: "pack.baseline", Name: "Cache Baseline", Run: run},
	)
	if !errors.Is(err, ErrDuplicateCheckID) {
		t.Fatalf("err = %v, want ErrDuplicateCheckID", err)
	}
	for _, want := range []string{`"pack.baseline"`, `"JWT Baseline"`, `"Cache Baseline"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}

	err = New().Register(Check{ID: "x", Run: run}, Check{ID: "x", Run: run})
	if err == nil || !strings.Contains(err.Error(), "check #1 and check #2") {
		t.Errorf("unnamed duplicates: err = %v", err)
	}
}

func TestCheckInvoke(t *testing.T) {
	ctx := context.Background()
	variants := Check{
		ID:          "v",
		Variants:    []string{"a", "b"},
		VariantMode: VariantsParallel,
		RunVariant: func(_ context.Context, _ Target, variant string, _ ResultStore) (Result, error) {
			if variant == "b" {
				return Result{Observations: []Observation{{Title: "hit"}}}, nil
			}
			return Result{}, nil
		},
	}
	r, err := variants.Invoke(ctx, Target{}, nil, NewStaticResultStore())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Attempts) != 2 || r.Attempts[1].Variant != "b" || len(r.Observations) != 1 || r.Observations[0].Variant != "b" {
		t.Errorf("result = %+v", r)
	}

	perRes := Check{ID: "p", Scope: ScopePerResource, RunResource: func(_ context.Context, _ Target, res Resource, _ ResultStore) (Result, error) {
		return Result{Data: res.ID}, nil
	}}
	if _, err := perRes.Invoke(ctx, Target{}, nil, NewStaticResultStore()); err == nil {
		t.Error("expected an error for a per-resource check without a resource")
	}
	r, err = perRes.Invoke(ctx, Target{}, &Resource{ID: "r"}, NewStaticResultStore())
	if err != nil || r.Data != "r" {
		t.Errorf("Invoke = %+v, %v", r, err)
	}

	if _, err := (Check{ID: "no-run"}).Invoke(ctx, Target{}, nil, NewStaticResultStore()); err == nil {
		t.Error("expected an error for a check without a run function")
	}
}

func TestAggregateAttempts(t *testing.T) {
	r := AggregateAttempts([]Attempt{{Variant: "a", Skipped: true, SkipReason: "no"}, {Variant: "b", Skipped: true}})
	if !r.Skipped || r.SkipReason != "no" || len(r.Attempts) != 2 {
		t.Errorf("result = %+v", r)
	}
}
