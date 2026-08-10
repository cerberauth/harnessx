package harnessx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	variantLower = "none"
	variantUpper = "NONE"
	variantMixed = "None"
	variantBad   = "bad"
)

// ── Unit tests: runVariants / runVariantAttempt / aggregateAttempts ───────────

func TestRunVariants_SequentialByDefault_PreservesOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string

	fn := func(_ context.Context, variant string) (Result, error) {
		mu.Lock()
		order = append(order, variant)
		mu.Unlock()
		return Result{}, nil
	}

	runVariants(context.Background(), []string{variantLower, variantUpper, variantMixed}, VariantsSequential, fn)

	want := []string{variantLower, variantUpper, variantMixed}
	if len(order) != len(want) {
		t.Fatalf("order length = %d, want %d", len(order), len(want))
	}
	for i, v := range want {
		if order[i] != v {
			t.Errorf("order[%d] = %q, want %q", i, order[i], v)
		}
	}
}

func TestRunVariants_ZeroValueModeIsSequential(t *testing.T) {
	var mode VariantMode
	if mode != VariantsSequential {
		t.Errorf("zero value of VariantMode should equal VariantsSequential")
	}
}

func TestRunVariants_ParallelMode_RunsAllVariants(t *testing.T) {
	variants := []string{"a", "b", "c", "d"}
	var count int32

	fn := func(_ context.Context, _ string) (Result, error) {
		atomic.AddInt32(&count, 1)
		time.Sleep(5 * time.Millisecond)
		return Result{}, nil
	}

	result := runVariants(context.Background(), variants, VariantsParallel, fn)

	if int(atomic.LoadInt32(&count)) != len(variants) {
		t.Errorf("expected all %d variants to run, got %d", len(variants), count)
	}
	if len(result.Attempts) != len(variants) {
		t.Errorf("Attempts length = %d, want %d", len(result.Attempts), len(variants))
	}
}

func TestRunVariants_AggregatesObservationsFromEachAttempt(t *testing.T) {
	fn := func(_ context.Context, variant string) (Result, error) {
		if variant == variantUpper {
			return Result{Observations: []Observation{{Title: "alg none accepted"}}}, nil
		}
		return Result{}, nil
	}

	result := runVariants(context.Background(), []string{variantLower, variantUpper, variantMixed}, VariantsSequential, fn)

	if len(result.Observations) != 1 {
		t.Fatalf("Observations length = %d, want 1", len(result.Observations))
	}
	if result.Observations[0].Variant != variantUpper {
		t.Errorf("Observation.Variant = %q, want %q", result.Observations[0].Variant, variantUpper)
	}
	if len(result.Attempts) != 3 {
		t.Fatalf("Attempts length = %d, want 3", len(result.Attempts))
	}
}

func TestRunVariants_ObservationVariantNotOverwrittenIfAlreadySet(t *testing.T) {
	fn := func(_ context.Context, _ string) (Result, error) {
		return Result{Observations: []Observation{{Title: "x", Variant: "custom"}}}, nil
	}

	result := runVariants(context.Background(), []string{"v1"}, VariantsSequential, fn)

	if result.Observations[0].Variant != "custom" {
		t.Errorf("Observation.Variant = %q, want %q (should not be overwritten)", result.Observations[0].Variant, "custom")
	}
}

func TestRunVariants_FirstErrorSurfacedOnAggregate(t *testing.T) {
	errBoom := errors.New("boom")
	fn := func(_ context.Context, variant string) (Result, error) {
		if variant == "b" {
			return Result{}, errBoom
		}
		return Result{}, nil
	}

	result := runVariants(context.Background(), []string{"a", "b", "c"}, VariantsSequential, fn)

	if !errors.Is(result.Err, errBoom) {
		t.Errorf("Result.Err = %v, want %v", result.Err, errBoom)
	}
	if len(result.Attempts) != 3 {
		t.Fatalf("Attempts length = %d, want 3", len(result.Attempts))
	}
	if result.Attempts[2].Err != nil {
		t.Errorf("attempt %q should still have run cleanly, got err %v", "c", result.Attempts[2].Err)
	}
}

func TestRunVariantAttempt_PanicIsRecoveredAndIsolated(t *testing.T) {
	fn := func(_ context.Context, variant string) (Result, error) {
		if variant == variantBad {
			panic("boom")
		}
		return Result{Observations: []Observation{{Title: "ok"}}}, nil
	}

	result := runVariants(context.Background(), []string{"good", variantBad, "good2"}, VariantsSequential, fn)

	if result.Err == nil {
		t.Fatal("expected aggregate Err to be set from the panicking attempt")
	}
	var scanErr *ScanError
	if !errors.As(result.Err, &scanErr) {
		t.Errorf("expected *ScanError, got %T: %v", result.Err, result.Err)
	}
	// The two successful attempts should still have produced observations.
	if len(result.Observations) != 2 {
		t.Errorf("Observations length = %d, want 2 (panic in one attempt should not lose the others)", len(result.Observations))
	}
	if len(result.Attempts) != 3 {
		t.Fatalf("Attempts length = %d, want 3", len(result.Attempts))
	}
}

// ── Integration tests: variants wired through Engine.Run ──────────────────────

func TestEngine_Variants_GlobalScope_RunsSequentiallyByDefault(t *testing.T) {
	rep := &testReporter{}
	e := New(WithReporters(rep), WithDefaultTimeout(5*time.Second))

	var mu sync.Mutex
	var seen []string

	chk := Check{
		ID:       "alg-none",
		Scope:    ScopeGlobal,
		Variants: []string{variantLower, variantUpper, variantMixed},
		RunVariant: func(_ context.Context, _ Target, variant string, _ ResultStore) (Result, error) {
			mu.Lock()
			seen = append(seen, variant)
			mu.Unlock()
			if variant == variantUpper {
				return Result{Observations: []Observation{{Title: "alg none accepted"}}}, nil
			}
			return Result{}, nil
		},
	}

	if err := e.Register(chk); err != nil {
		t.Fatalf("Register: %v", err)
	}

	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(seen) != 3 {
		t.Fatalf("expected 3 variant attempts, got %d: %v", len(seen), seen)
	}
	if seen[0] != variantLower || seen[1] != variantUpper || seen[2] != variantMixed {
		t.Errorf("sequential order not preserved: %v", seen)
	}
	if len(summary.Observations) != 1 {
		t.Fatalf("summary.Observations length = %d, want 1", len(summary.Observations))
	}
	if summary.Observations[0].Variant != variantUpper {
		t.Errorf("Observation.Variant = %q, want %q", summary.Observations[0].Variant, variantUpper)
	}

	if len(summary.Results) != 1 {
		t.Fatalf("summary.Results length = %d, want 1", len(summary.Results))
	}
	if len(summary.Results[0].Attempts) != 3 {
		t.Errorf("Result.Attempts length = %d, want 3", len(summary.Results[0].Attempts))
	}
}

func TestEngine_Variants_GlobalScope_ParallelMode(t *testing.T) {
	e := New(WithDefaultTimeout(5 * time.Second))

	var count int32
	chk := Check{
		ID:          "parallel-variants",
		Scope:       ScopeGlobal,
		Variants:    []string{"a", "b", "c"},
		VariantMode: VariantsParallel,
		RunVariant: func(_ context.Context, _ Target, _ string, _ ResultStore) (Result, error) {
			atomic.AddInt32(&count, 1)
			time.Sleep(5 * time.Millisecond)
			return Result{}, nil
		},
	}

	if err := e.Register(chk); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := e.Run(context.Background(), testTarget); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if atomic.LoadInt32(&count) != 3 {
		t.Errorf("expected all 3 variants to run, got %d", count)
	}
}

func TestEngine_Variants_ResourceScope(t *testing.T) {
	rep := &testReporter{}
	e := New(WithReporters(rep), WithDefaultTimeout(5*time.Second))

	resources := []Resource{{ID: "r1"}, {ID: "r2"}}
	recon := Check{
		ID: checkIDRecon,
		Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
			return Result{CheckID: checkIDRecon, Resources: resources}, nil
		},
	}

	var mu sync.Mutex
	calls := 0

	probe := Check{
		ID:        checkIDEndpoint,
		Scope:     ScopePerResource,
		DependsOn: []CheckID{checkIDRecon},
		Variants:  []string{"v1", "v2"},
		RunResourceVariant: func(_ context.Context, _ Target, _ Resource, _ string, _ ResultStore) (Result, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			return Result{}, nil
		},
	}

	if err := e.Register(recon, probe); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := e.Run(context.Background(), testTarget); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if calls != len(resources)*2 {
		t.Errorf("expected %d variant calls (2 resources x 2 variants), got %d", len(resources)*2, calls)
	}
}

func TestEngine_Variants_GlobalScope_MissingRunVariant(t *testing.T) {
	e := New(WithDefaultTimeout(time.Second))
	chk := Check{
		ID:       "broken",
		Scope:    ScopeGlobal,
		Variants: []string{"a"},
	}
	if err := e.Register(chk); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err := e.Run(context.Background(), testTarget)
	if err == nil {
		t.Fatal("expected error for missing RunVariant, got nil")
	}
}

func TestEngine_Variants_ResourceScope_MissingRunResourceVariant(t *testing.T) {
	e := New(WithDefaultTimeout(time.Second))
	chk := Check{
		ID:       "broken",
		Scope:    ScopePerResource,
		Variants: []string{"a"},
	}
	if err := e.Register(chk); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err := e.Run(context.Background(), testTarget)
	if err == nil {
		t.Fatal("expected error for missing RunResourceVariant, got nil")
	}
}

func TestEngine_NonVariantChecks_StillWorkUnchanged(t *testing.T) {
	e := New(WithDefaultTimeout(time.Second))
	chk := Check{
		ID: "plain",
		Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
			return Result{}, nil
		},
	}
	if err := e.Register(chk); err != nil {
		t.Fatalf("Register: %v", err)
	}
	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Executed != 1 {
		t.Errorf("Executed = %d, want 1", summary.Executed)
	}
	if len(summary.Results[0].Attempts) != 0 {
		t.Errorf("non-variant check should have no Attempts, got %d", len(summary.Results[0].Attempts))
	}
}
