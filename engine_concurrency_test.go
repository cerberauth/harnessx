package harnessx

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// Concurrent RunScenario calls on the same engine must not leak results across calls.
func TestEngine_ConcurrentRunScenario_Isolated(t *testing.T) {
	engine := New()

	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	summaries := make([]ScanSummary, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			checkID := CheckID(fmt.Sprintf("check-%d", i))
			target := Target{URL: fmt.Sprintf("http://example.com/%d", i), Host: testHost}
			scenario := Scenario{
				ID: fmt.Sprintf("scenario-%d", i),
				Checks: []Check{
					{
						ID:    checkID,
						Scope: ScopeGlobal,
						Run: func(_ context.Context, tgt Target, _ ResultStore) (Result, error) {
							if tgt.URL != target.URL {
								return Result{}, fmt.Errorf("got target %q, want %q", tgt.URL, target.URL)
							}
							return Result{}, nil
						},
					},
				},
			}

			summary, err := engine.RunScenario(context.Background(), target, scenario)
			errs[i] = err
			summaries[i] = summary
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Errorf("call %d: unexpected error: %v", i, errs[i])
		}
		if summaries[i].Executed != 1 {
			t.Errorf("call %d: Executed = %d, want 1", i, summaries[i].Executed)
		}
		if len(summaries[i].Results) != 1 {
			t.Fatalf("call %d: len(Results) = %d, want 1", i, len(summaries[i].Results))
		}
		wantID := CheckID(fmt.Sprintf("check-%d", i))
		if got := summaries[i].Results[0].CheckID; got != wantID {
			t.Errorf("call %d: got result for check %q, want %q", i, got, wantID)
		}
	}
}

// A check's Run callback may call RunScenario on the same engine (nested scenario) without deadlocking.
func TestEngine_ReentrantRunScenario(t *testing.T) {
	engine := New()

	innerRan := false
	outerRan := false

	outerScenario := Scenario{
		ID: "outer",
		Checks: []Check{
			{
				ID:    "outer-check",
				Scope: ScopeGlobal,
				Run: func(ctx context.Context, target Target, _ ResultStore) (Result, error) {
					outerRan = true

					innerScenario := Scenario{
						ID: "inner",
						Checks: []Check{
							{
								ID:    "inner-check",
								Scope: ScopeGlobal,
								Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
									innerRan = true
									return Result{}, nil
								},
							},
						},
					}

					innerSummary, err := engine.RunScenario(ctx, target, innerScenario)
					if err != nil {
						return Result{}, err
					}
					if innerSummary.Executed != 1 {
						return Result{}, fmt.Errorf("inner scenario Executed = %d, want 1", innerSummary.Executed)
					}
					return Result{}, nil
				},
			},
		},
	}

	summary, err := engine.RunScenario(context.Background(), testTarget, outerScenario)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Executed != 1 {
		t.Errorf("outer Executed = %d, want 1", summary.Executed)
	}
	if !outerRan {
		t.Error("outer check did not run")
	}
	if !innerRan {
		t.Error("inner (nested RunScenario) check did not run")
	}
}

// Concurrent RunScenario calls with different Scenario.Reporters must only receive their own callbacks.
func TestEngine_ConcurrentRunScenario_PerCallReporters(t *testing.T) {
	engine := New()

	repA := &testReporter{}
	repB := &testReporter{}

	scenarioFor := func(id CheckID, rep Reporter) Scenario {
		return Scenario{
			ID: string(id),
			Checks: []Check{
				{
					ID:    id,
					Scope: ScopeGlobal,
					Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
						return Result{}, nil
					},
				},
			},
			Reporters: []Reporter{rep},
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = engine.RunScenario(context.Background(), testTarget, scenarioFor("a", repA))
	}()
	go func() {
		defer wg.Done()
		_, _ = engine.RunScenario(context.Background(), testTarget, scenarioFor("b", repB))
	}()
	wg.Wait()

	repA.mu.Lock()
	if len(repA.completes) != 1 || repA.completes[0].CheckID != "a" {
		t.Errorf("repA.completes = %v, want exactly check 'a'", repA.completes)
	}
	repA.mu.Unlock()

	repB.mu.Lock()
	if len(repB.completes) != 1 || repB.completes[0].CheckID != "b" {
		t.Errorf("repB.completes = %v, want exactly check 'b'", repB.completes)
	}
	repB.mu.Unlock()
}
