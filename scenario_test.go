package harnessx

import (
	"context"
	"errors"
	"testing"
)

func TestRunScenario_Basic(t *testing.T) {
	engine := New()

	ran := make(map[CheckID]bool)
	scenario := Scenario{
		ID:   "basic",
		Name: "Basic",
		Checks: []Check{
			{
				ID:    "a",
				Scope: ScopeGlobal,
				Run: func(ctx context.Context, _ Target, _ ResultStore) (Result, error) {
					ran["a"] = true
					return Result{}, nil
				},
			},
			{
				ID:        "b",
				Scope:     ScopeGlobal,
				DependsOn: []CheckID{"a"},
				Run: func(ctx context.Context, _ Target, _ ResultStore) (Result, error) {
					ran["b"] = true
					return Result{}, nil
				},
			},
		},
	}

	summary, err := engine.RunScenario(context.Background(), testTarget, scenario)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Executed != 2 {
		t.Errorf("Executed = %d, want 2", summary.Executed)
	}
	if !ran["a"] || !ran["b"] {
		t.Errorf("not all checks ran: %v", ran)
	}
}

func TestRunScenario_DoesNotRunRegisteredChecks(t *testing.T) {
	engine := New()
	_ = engine.Register(Check{
		ID:    "x",
		Scope: ScopeGlobal,
		Run: func(ctx context.Context, _ Target, _ ResultStore) (Result, error) {
			return Result{}, nil
		},
	})

	scenario := Scenario{
		Checks: []Check{
			{
				ID:    "a",
				Scope: ScopeGlobal,
				Run:   func(_ context.Context, _ Target, _ ResultStore) (Result, error) { return Result{}, nil },
			},
			{
				ID:        "b",
				Scope:     ScopeGlobal,
				DependsOn: []CheckID{"a"},
				Run:       func(_ context.Context, _ Target, _ ResultStore) (Result, error) { return Result{}, nil },
			},
		},
	}

	summary, err := engine.RunScenario(context.Background(), testTarget, scenario)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.TotalChecks != 2 {
		t.Errorf("TotalChecks = %d, want 2", summary.TotalChecks)
	}
	for _, r := range summary.Results {
		if r.CheckID == "x" {
			t.Error("registered check 'x' ran inside RunScenario")
		}
	}
}

func TestRunScenario_EmptyScenario(t *testing.T) {
	rep := &testReporter{}
	engine := New(WithReporters(rep))

	_, err := engine.RunScenario(context.Background(), testTarget, Scenario{})
	if !errors.Is(err, ErrNoChecks) {
		t.Errorf("want ErrNoChecks, got %v", err)
	}
	if rep.getSummary() == nil {
		t.Error("OnScanComplete not called on empty scenario")
	}
}

func TestRunScenario_DependencyOrderRespected(t *testing.T) {
	bSawA := false

	scenario := Scenario{
		Checks: []Check{
			{
				ID:    "a",
				Scope: ScopeGlobal,
				Run:   func(_ context.Context, _ Target, _ ResultStore) (Result, error) { return Result{}, nil },
			},
			{
				ID:        "b",
				Scope:     ScopeGlobal,
				DependsOn: []CheckID{"a"},
				Run: func(_ context.Context, _ Target, store ResultStore) (Result, error) {
					_, ok := store.Get("a")
					bSawA = ok
					return Result{}, nil
				},
			},
		},
	}

	_, err := New().RunScenario(context.Background(), testTarget, scenario)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bSawA {
		t.Error("check 'b' did not see 'a' result in store")
	}
}

func TestRunScenario_SharedFuncAcrossScenarios(t *testing.T) {
	const gqlIntrospection CheckID = "gql-introspection"

	authFn := func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
		return Result{}, nil
	}

	restScenario := Scenario{
		ID: "rest-api",
		Checks: []Check{
			{
				ID:    "rest-discovery",
				Scope: ScopeGlobal,
				Run:   func(_ context.Context, _ Target, _ ResultStore) (Result, error) { return Result{}, nil },
			},
			{
				ID:        "rest-auth",
				Scope:     ScopeGlobal,
				DependsOn: []CheckID{"rest-discovery"},
				Run:       authFn,
			},
		},
	}

	gqlScenario := Scenario{
		ID: "graphql-api",
		Checks: []Check{
			{
				ID:    gqlIntrospection,
				Scope: ScopeGlobal,
				Run:   func(_ context.Context, _ Target, _ ResultStore) (Result, error) { return Result{}, nil },
			},
			{
				ID:        "gql-auth",
				Scope:     ScopeGlobal,
				DependsOn: []CheckID{gqlIntrospection},
				Run:       authFn,
			},
		},
	}

	engine := New()

	restSummary, err := engine.RunScenario(context.Background(), testTarget, restScenario)
	if err != nil {
		t.Fatalf("rest scenario error: %v", err)
	}
	if restSummary.Executed != 2 {
		t.Errorf("REST Executed = %d, want 2", restSummary.Executed)
	}

	gqlSummary, err := engine.RunScenario(context.Background(), testTarget, gqlScenario)
	if err != nil {
		t.Fatalf("graphql scenario error: %v", err)
	}
	if gqlSummary.Executed != 2 {
		t.Errorf("GraphQL Executed = %d, want 2", gqlSummary.Executed)
	}

	for _, r := range restSummary.Results {
		if r.CheckID == gqlIntrospection || r.CheckID == "gql-auth" {
			t.Errorf("REST scenario ran GraphQL check %q", r.CheckID)
		}
	}
}

func TestRunScenario_CycleDetected(t *testing.T) {
	scenario := Scenario{
		Checks: []Check{
			{ID: "a", Scope: ScopeGlobal, DependsOn: []CheckID{"b"}, Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) { return Result{}, nil }},
			{ID: "b", Scope: ScopeGlobal, DependsOn: []CheckID{"a"}, Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) { return Result{}, nil }},
		},
	}

	_, err := New().RunScenario(context.Background(), testTarget, scenario)
	if !errors.Is(err, ErrCycleDetected) {
		t.Errorf("want ErrCycleDetected, got %v", err)
	}
}

func TestRunScenario_UnknownDependency(t *testing.T) {
	scenario := Scenario{
		Checks: []Check{
			{ID: "a", Scope: ScopeGlobal, DependsOn: []CheckID{"missing"}, Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) { return Result{}, nil }},
		},
	}

	_, err := New().RunScenario(context.Background(), testTarget, scenario)
	if !errors.Is(err, ErrUnknownDependency) {
		t.Errorf("want ErrUnknownDependency, got %v", err)
	}
}

func TestRunScenario_ReporterCallbacks(t *testing.T) {
	rep := &testReporter{}
	engine := New(WithReporters(rep))

	scenario := Scenario{
		Checks: []Check{
			{
				ID:    "a",
				Scope: ScopeGlobal,
				Run:   func(_ context.Context, _ Target, _ ResultStore) (Result, error) { return Result{}, nil },
			},
			{
				ID:        "b",
				Scope:     ScopeGlobal,
				DependsOn: []CheckID{"a"},
				Run:       func(_ context.Context, _ Target, _ ResultStore) (Result, error) { return Result{}, nil },
			},
		},
	}

	_, err := engine.RunScenario(context.Background(), testTarget, scenario)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rep.mu.Lock()
	defer rep.mu.Unlock()

	if rep.totalChecks != 2 {
		t.Errorf("OnScanStart totalChecks = %d, want 2", rep.totalChecks)
	}
	if len(rep.starts) != 2 {
		t.Errorf("OnCheckStart called %d times, want 2", len(rep.starts))
	}
	if len(rep.completes) != 2 {
		t.Errorf("OnCheckComplete called %d times, want 2", len(rep.completes))
	}
	if rep.summary == nil {
		t.Error("OnScanComplete not called")
	}
}
