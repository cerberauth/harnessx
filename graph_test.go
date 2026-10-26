package harnessx

import (
	"context"
	"errors"
	"testing"
)

func stubRun(_ context.Context, _ Target, _ ResultStore) (Result, error) {
	return Result{}, nil
}

func stubRunResource(_ context.Context, _ Target, _ Resource, _ ResultStore) (Result, error) {
	return Result{}, nil
}

func TestLevels_Linear(t *testing.T) {
	// A → B → C
	a := Check{ID: "A", Run: stubRun}
	b := Check{ID: "B", Run: stubRun, DependsOn: []CheckID{"A"}}
	c := Check{ID: "C", Run: stubRun, DependsOn: []CheckID{"B"}}

	got, err := levels([]Check{a, b, c})
	if err != nil {
		t.Fatalf("levels: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 levels, got %d", len(got))
	}
	if got[0][0].ID != "A" {
		t.Errorf("level 0 expected A, got %s", got[0][0].ID)
	}
	if got[1][0].ID != "B" {
		t.Errorf("level 1 expected B, got %s", got[1][0].ID)
	}
	if got[2][0].ID != "C" {
		t.Errorf("level 2 expected C, got %s", got[2][0].ID)
	}
}

func TestLevels_Parallel(t *testing.T) {
	// A has no deps; B and C both depend on A → level 0: [A], level 1: [B, C]
	a := Check{ID: "A", Run: stubRun}
	b := Check{ID: "B", Run: stubRun, DependsOn: []CheckID{"A"}}
	c := Check{ID: "C", Run: stubRun, DependsOn: []CheckID{"A"}}

	got, err := levels([]Check{a, b, c})
	if err != nil {
		t.Fatalf("levels: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 levels, got %d", len(got))
	}
	if len(got[0]) != 1 || got[0][0].ID != "A" {
		t.Errorf("level 0 should be [A]")
	}
	if len(got[1]) != 2 {
		t.Errorf("level 1 should have 2 checks, got %d", len(got[1]))
	}
}

func TestLevels_CycleDetected(t *testing.T) {
	// A → B → A (cycle)
	a := Check{ID: "A", Run: stubRun, DependsOn: []CheckID{"B"}}
	b := Check{ID: "B", Run: stubRun, DependsOn: []CheckID{"A"}}

	_, err := levels([]Check{a, b})
	if !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("expected ErrCycleDetected, got %v", err)
	}
}

func TestLevels_UnknownDependency(t *testing.T) {
	a := Check{ID: "A", Run: stubRun, DependsOn: []CheckID{"MISSING"}}
	_, err := levels([]Check{a})
	if !errors.Is(err, ErrUnknownDependency) {
		t.Fatalf("expected ErrUnknownDependency, got %v", err)
	}
}

func TestLevels_NoDeps(t *testing.T) {
	checks := []Check{
		{ID: "X", Run: stubRun},
		{ID: "Y", Run: stubRun},
		{ID: "Z", Run: stubRun},
	}
	got, err := levels(checks)
	if err != nil {
		t.Fatalf("levels: %v", err)
	}
	// All independent → single level with 3 checks.
	if len(got) != 1 {
		t.Fatalf("expected 1 level, got %d", len(got))
	}
	if len(got[0]) != 3 {
		t.Errorf("expected 3 checks in level 0, got %d", len(got[0]))
	}
}
