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

func TestGraphTopoSort_Linear(t *testing.T) {
	// A → B → C
	a := Check{ID: "A", Run: stubRun}
	b := Check{ID: "B", Run: stubRun, DependsOn: []CheckID{"A"}}
	c := Check{ID: "C", Run: stubRun, DependsOn: []CheckID{"B"}}

	g, err := newGraph([]Check{a, b, c})
	if err != nil {
		t.Fatalf("newGraph: %v", err)
	}
	levels, err := g.topoSort()
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}

	if len(levels) != 3 {
		t.Fatalf("expected 3 levels, got %d", len(levels))
	}
	if levels[0][0].ID != "A" {
		t.Errorf("level 0 expected A, got %s", levels[0][0].ID)
	}
	if levels[1][0].ID != "B" {
		t.Errorf("level 1 expected B, got %s", levels[1][0].ID)
	}
	if levels[2][0].ID != "C" {
		t.Errorf("level 2 expected C, got %s", levels[2][0].ID)
	}
}

func TestGraphTopoSort_Parallel(t *testing.T) {
	// A has no deps; B and C both depend on A → level 0: [A], level 1: [B, C]
	a := Check{ID: "A", Run: stubRun}
	b := Check{ID: "B", Run: stubRun, DependsOn: []CheckID{"A"}}
	c := Check{ID: "C", Run: stubRun, DependsOn: []CheckID{"A"}}

	g, err := newGraph([]Check{a, b, c})
	if err != nil {
		t.Fatalf("newGraph: %v", err)
	}
	levels, err := g.topoSort()
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}

	if len(levels) != 2 {
		t.Fatalf("expected 2 levels, got %d", len(levels))
	}
	if len(levels[0]) != 1 || levels[0][0].ID != "A" {
		t.Errorf("level 0 should be [A]")
	}
	if len(levels[1]) != 2 {
		t.Errorf("level 1 should have 2 checks, got %d", len(levels[1]))
	}
}

func TestGraphTopoSort_CycleDetected(t *testing.T) {
	// A → B → A (cycle)
	a := Check{ID: "A", Run: stubRun, DependsOn: []CheckID{"B"}}
	b := Check{ID: "B", Run: stubRun, DependsOn: []CheckID{"A"}}

	g, err := newGraph([]Check{a, b})
	if err != nil {
		t.Fatalf("newGraph: %v", err)
	}
	_, err = g.topoSort()
	if !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("expected ErrCycleDetected, got %v", err)
	}
}

func TestGraphNewGraph_UnknownDependency(t *testing.T) {
	a := Check{ID: "A", Run: stubRun, DependsOn: []CheckID{"MISSING"}}
	_, err := newGraph([]Check{a})
	if !errors.Is(err, ErrUnknownDependency) {
		t.Fatalf("expected ErrUnknownDependency, got %v", err)
	}
}

func TestGraphTopoSort_NoDeps(t *testing.T) {
	checks := []Check{
		{ID: "X", Run: stubRun},
		{ID: "Y", Run: stubRun},
		{ID: "Z", Run: stubRun},
	}
	g, err := newGraph(checks)
	if err != nil {
		t.Fatalf("newGraph: %v", err)
	}
	levels, err := g.topoSort()
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	// All independent → single level with 3 checks.
	if len(levels) != 1 {
		t.Fatalf("expected 1 level, got %d", len(levels))
	}
	if len(levels[0]) != 3 {
		t.Errorf("expected 3 checks in level 0, got %d", len(levels[0]))
	}
}
