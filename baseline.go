package harnessx

import (
	"context"
	"fmt"
	"time"
)

// Snapshot is a captured response, reduced to whatever a comparator needs to
// judge it. StatusCode is the convenience field the default comparator uses;
// Data carries anything else (headers, body, timing, a protocol-specific
// type such as probe.Attempt) for custom comparators to inspect.
type Snapshot struct {
	StatusCode int
	Data       any
}

// Baseline is a Snapshot held up as the expected/reference response for a resource.
type Baseline = Snapshot

// BaselineSource resolves the baseline for a resource. It returns ok=false
// when no baseline is available, in which case the comparison check skips.
type BaselineSource func(ctx context.Context, target Target, resource Resource, store ResultStore) (Baseline, bool)

// StaticBaseline returns a BaselineSource that always resolves to the same
// fixed baseline, regardless of target or resource.
func StaticBaseline(b Baseline) BaselineSource {
	return func(_ context.Context, _ Target, _ Resource, _ ResultStore) (Baseline, bool) {
		return b, true
	}
}

// BaselineFromResource returns a BaselineSource that reads a baseline
// manually attached to Resource.Data at discovery time.
func BaselineFromResource() BaselineSource {
	return func(_ context.Context, _ Target, resource Resource, _ ResultStore) (Baseline, bool) {
		b, ok := resource.Data.(Baseline)
		return b, ok
	}
}

// BaselineFromCheck returns a BaselineSource that reads the baseline captured
// for a resource by a prior "baseline probe" check (see CaptureBaselineCheck),
// via that check's per-resource Result.Data.
func BaselineFromCheck(id CheckID) BaselineSource {
	return func(_ context.Context, _ Target, resource Resource, store ResultStore) (Baseline, bool) {
		r, ok := store.GetForResource(id, resource.ID)
		if !ok {
			return Baseline{}, false
		}
		b, ok := r.Data.(Baseline)
		return b, ok
	}
}

// BaselineComparator compares a baseline against a freshly captured snapshot
// and returns any resulting Observations. A nil or empty result means no
// deviation was found.
type BaselineComparator func(baseline, current Snapshot) []Observation

// CompareStatusCode is the default BaselineComparator: it flags any change
// in status code between the baseline and the current snapshot.
func CompareStatusCode(baseline, current Snapshot) []Observation {
	if baseline.StatusCode == current.StatusCode {
		return nil
	}
	return []Observation{{
		Title:       "Response deviates from baseline",
		Description: fmt.Sprintf("baseline status was %d, got %d", baseline.StatusCode, current.StatusCode),
	}}
}

// BaselineCheckConfig configures a baseline-comparison check built by NewBaselineCheck.
type BaselineCheckConfig struct {
	ID          CheckID
	Name        string
	Description string
	DependsOn   []CheckID

	// Baseline resolves the expected response for a resource.
	Baseline BaselineSource

	// Capture performs the live attempt and returns its snapshot.
	Capture func(ctx context.Context, target Target, resource Resource, store ResultStore) (Snapshot, error)

	// Compare judges the baseline against the captured snapshot.
	// Defaults to CompareStatusCode when nil.
	Compare BaselineComparator

	Timeout     time.Duration
	Concurrency int
}

// NewBaselineCheck builds a ScopePerResource Check that resolves cfg.Baseline,
// captures the current response via cfg.Capture, and compares them via
// cfg.Compare (CompareStatusCode by default). A resource with no resolvable
// baseline is skipped rather than compared.
func NewBaselineCheck(cfg BaselineCheckConfig) Check {
	compare := cfg.Compare
	if compare == nil {
		compare = CompareStatusCode
	}

	return Check{
		ID:          cfg.ID,
		Name:        cfg.Name,
		Description: cfg.Description,
		DependsOn:   cfg.DependsOn,
		Scope:       ScopePerResource,
		Timeout:     cfg.Timeout,
		Concurrency: cfg.Concurrency,
		RunResource: func(ctx context.Context, target Target, resource Resource, store ResultStore) (Result, error) {
			baseline, ok := cfg.Baseline(ctx, target, resource, store)
			if !ok {
				return Result{Skipped: true, SkipReason: "no baseline available for resource"}, nil
			}

			current, err := cfg.Capture(ctx, target, resource, store)
			if err != nil {
				return Result{}, err
			}

			obs := compare(baseline, current)
			for i := range obs {
				obs[i].CheckID = cfg.ID
				obs[i].ResourceID = resource.ID
			}
			return Result{Observations: obs}, nil
		},
	}
}

// CaptureBaselineCheck builds a ScopePerResource Check that captures a
// Snapshot per resource via capture and stores it as Result.Data — the
// "baseline probe". Pair it with BaselineFromCheck(id) and a DependsOn on
// this check's id in the comparison check.
func CaptureBaselineCheck(id CheckID, name string, capture func(ctx context.Context, target Target, resource Resource, store ResultStore) (Snapshot, error)) Check {
	return Check{
		ID:    id,
		Name:  name,
		Scope: ScopePerResource,
		RunResource: func(ctx context.Context, target Target, resource Resource, store ResultStore) (Result, error) {
			snap, err := capture(ctx, target, resource, store)
			if err != nil {
				return Result{}, err
			}
			return Result{Data: snap}, nil
		},
	}
}

// GlobalBaselineSource is the ScopeGlobal counterpart of BaselineSource: it
// resolves the Baseline to compare against for a target with no per-resource
// dimension.
type GlobalBaselineSource func(ctx context.Context, target Target, store ResultStore) (Baseline, bool)

// GlobalCapture is the ScopeGlobal counterpart of the Capture func used by
// BaselineCheckConfig: it captures the current Snapshot for a target with no
// per-resource dimension.
type GlobalCapture func(ctx context.Context, target Target, store ResultStore) (Snapshot, error)

// StaticGlobalBaseline is the ScopeGlobal counterpart of StaticBaseline.
func StaticGlobalBaseline(b Baseline) GlobalBaselineSource {
	return func(_ context.Context, _ Target, _ ResultStore) (Baseline, bool) {
		return b, true
	}
}

// BaselineFromGlobalCheck is the ScopeGlobal counterpart of BaselineFromCheck:
// it reads the baseline from the global (non-per-resource) result of a prior
// check (see CaptureGlobalBaselineCheck).
func BaselineFromGlobalCheck(id CheckID) GlobalBaselineSource {
	return func(_ context.Context, _ Target, store ResultStore) (Baseline, bool) {
		return GetData[Baseline](store, id)
	}
}

// GlobalBaselineCheckConfig is the ScopeGlobal counterpart of
// BaselineCheckConfig.
type GlobalBaselineCheckConfig struct {
	ID          CheckID
	Name        string
	Description string
	DependsOn   []CheckID

	Baseline GlobalBaselineSource

	Capture GlobalCapture

	Compare BaselineComparator

	Timeout time.Duration
}

// NewGlobalBaselineCheck is the ScopeGlobal counterpart of NewBaselineCheck,
// for targets with no per-resource dimension (see BaselineCheckConfig for the
// ScopePerResource variant).
func NewGlobalBaselineCheck(cfg GlobalBaselineCheckConfig) Check {
	compare := cfg.Compare
	if compare == nil {
		compare = CompareStatusCode
	}

	return Check{
		ID:          cfg.ID,
		Name:        cfg.Name,
		Description: cfg.Description,
		DependsOn:   cfg.DependsOn,
		Scope:       ScopeGlobal,
		Timeout:     cfg.Timeout,
		Run: func(ctx context.Context, target Target, store ResultStore) (Result, error) {
			baseline, ok := cfg.Baseline(ctx, target, store)
			if !ok {
				return Result{Skipped: true, SkipReason: "no baseline available for target"}, nil
			}

			current, err := cfg.Capture(ctx, target, store)
			if err != nil {
				return Result{}, err
			}

			obs := compare(baseline, current)
			for i := range obs {
				obs[i].CheckID = cfg.ID
			}
			return Result{Observations: obs}, nil
		},
	}
}

// CaptureGlobalBaselineCheck is the ScopeGlobal counterpart of
// CaptureBaselineCheck, for targets with no per-resource dimension.
func CaptureGlobalBaselineCheck(id CheckID, name string, capture GlobalCapture) Check {
	return Check{
		ID:    id,
		Name:  name,
		Scope: ScopeGlobal,
		Run: func(ctx context.Context, target Target, store ResultStore) (Result, error) {
			snap, err := capture(ctx, target, store)
			if err != nil {
				return Result{}, err
			}
			return Result{Data: snap}, nil
		},
	}
}
