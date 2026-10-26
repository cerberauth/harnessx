package harnessx

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cerberauth/harnessx/probe"
)

// Snapshot is a captured response, reduced to whatever a comparator needs to
// judge it. StatusCode is the convenience field the default comparator uses;
// Header, Body, and Duration carry the full response for comparators that
// need more; Data carries anything else custom comparators want to inspect.
type Snapshot struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Duration   time.Duration
	Data       any

	// Truncated reports that Body was cut at the probe's response size cap
	// (see probe.DefaultMaxBodySize) and is not the complete response.
	Truncated bool

	// BodyHash is the SimHash of Body, set by CaptureSnapshot and
	// ProbeAndCompareBaseline. Zero means unset; CompareSimilarity then hashes
	// Body itself.
	BodyHash uint64
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
	return statusDeviation(baseline, current)
}

// CaptureSnapshot sends the request returned by build via probe.DoResponse and
// returns the response as a Snapshot: status, headers, body (capped at
// probe.DefaultMaxBodySize, with Truncated set when cut), duration and
// BodyHash. It is the building block for a baseline — typically a request to
// a random, certainly-absent path, so a catch-all "soft 404" answer can be
// told apart from a real page with CompareSimilarity.
func CaptureSnapshot(ctx context.Context, p *probe.Probe, build probe.RequestBuilder) (Snapshot, error) {
	req, err := build(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	resp, err := probe.DoResponse(ctx, p.Client(), req)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       resp.Body,
		Duration:   resp.Duration,
		Truncated:  resp.Truncated,
		BodyHash:   SimHash(resp.Body),
	}, nil
}

// ProbeAndCompareBaseline captures the request returned by build (see
// CaptureSnapshot) and compares the resulting snapshot against the baseline
// stored under baselineID. It returns the current snapshot and whether the
// response deviated from the baseline.
//
// If no baseline is stored under baselineID it returns the current snapshot, no
// deviation, and ErrNoBaseline, rather than comparing against an empty
// Snapshot.
//
// With no comparators, only a change in status code counts as a deviation
// (CompareStatusCode). Otherwise every comparator is applied and the response
// deviates if any of them reports an observation, so comparators compose.
func ProbeAndCompareBaseline(ctx context.Context, p *probe.Probe, build probe.RequestBuilder, store ResultStore, baselineID CheckID, comparators ...BaselineComparator) (Snapshot, bool, error) {
	current, err := CaptureSnapshot(ctx, p, build)
	if err != nil {
		return Snapshot{}, false, err
	}

	if len(comparators) == 0 {
		comparators = []BaselineComparator{CompareStatusCode}
	}
	baseline, ok := GetData[Snapshot](store, baselineID)
	if !ok {
		return current, false, fmt.Errorf("%w: check %q", ErrNoBaseline, baselineID)
	}
	for _, compare := range comparators {
		if len(compare(baseline, current)) > 0 {
			return current, true, nil
		}
	}
	return current, false, nil
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

			return Result{Observations: compareSnapshots(cfg.Compare, baseline, current, cfg.ID, resource.ID)}, nil
		},
	}
}

// compareSnapshots runs compare (CompareStatusCode when nil) and stamps the
// resulting observations with the check and resource they belong to. It is the
// comparison step shared by the per-resource and global baseline checks.
func compareSnapshots(compare BaselineComparator, baseline, current Snapshot, id CheckID, resourceID string) []Observation {
	if compare == nil {
		compare = CompareStatusCode
	}
	obs := compare(baseline, current)
	for i := range obs {
		obs[i].CheckID = id
		obs[i].ResourceID = resourceID
	}
	return obs
}

// captureResult turns a captured Snapshot into the Result of a baseline probe,
// carrying it as Data for BaselineFromCheck / BaselineFromGlobalCheck.
func captureResult(snap Snapshot, err error) (Result, error) {
	if err != nil {
		return Result{}, err
	}
	return Result{Data: snap}, nil
}

// CaptureBaselineCheckConfig configures a baseline-capture check built by
// NewCaptureBaselineCheck.
type CaptureBaselineCheckConfig struct {
	ID          CheckID
	Name        string
	Description string

	// DependsOn must include the check that discovers the resources, or the
	// capture check finds none and is skipped.
	DependsOn []CheckID

	// Capture performs the live attempt for one resource and returns its snapshot.
	Capture func(ctx context.Context, target Target, resource Resource, store ResultStore) (Snapshot, error)

	Timeout     time.Duration
	Concurrency int
}

// NewCaptureBaselineCheck builds a ScopePerResource Check that captures a
// Snapshot per resource via cfg.Capture and stores it as Result.Data — the
// "baseline probe". Pair it with BaselineFromCheck(cfg.ID) and a DependsOn on
// this check's ID in the comparison check.
func NewCaptureBaselineCheck(cfg CaptureBaselineCheckConfig) Check {
	return Check{
		ID:          cfg.ID,
		Name:        cfg.Name,
		Description: cfg.Description,
		DependsOn:   cfg.DependsOn,
		Scope:       ScopePerResource,
		Timeout:     cfg.Timeout,
		Concurrency: cfg.Concurrency,
		RunResource: func(ctx context.Context, target Target, resource Resource, store ResultStore) (Result, error) {
			return captureResult(cfg.Capture(ctx, target, resource, store))
		},
	}
}

// CaptureBaselineCheck is NewCaptureBaselineCheck with only an ID, a name and a
// capture function. The returned Check has no DependsOn, so set DependsOn on it
// to the check that discovers the resources before registering it.
func CaptureBaselineCheck(id CheckID, name string, capture func(ctx context.Context, target Target, resource Resource, store ResultStore) (Snapshot, error)) Check {
	return NewCaptureBaselineCheck(CaptureBaselineCheckConfig{ID: id, Name: name, Capture: capture})
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

			return Result{Observations: compareSnapshots(cfg.Compare, baseline, current, cfg.ID, "")}, nil
		},
	}
}

// CaptureGlobalBaselineCheckConfig is the ScopeGlobal counterpart of
// CaptureBaselineCheckConfig.
type CaptureGlobalBaselineCheckConfig struct {
	ID          CheckID
	Name        string
	Description string
	DependsOn   []CheckID

	// Capture performs the live attempt and returns its snapshot.
	Capture GlobalCapture

	Timeout time.Duration
}

// NewCaptureGlobalBaselineCheck is the ScopeGlobal counterpart of
// NewCaptureBaselineCheck, for targets with no per-resource dimension.
func NewCaptureGlobalBaselineCheck(cfg CaptureGlobalBaselineCheckConfig) Check {
	return Check{
		ID:          cfg.ID,
		Name:        cfg.Name,
		Description: cfg.Description,
		DependsOn:   cfg.DependsOn,
		Scope:       ScopeGlobal,
		Timeout:     cfg.Timeout,
		Run: func(ctx context.Context, target Target, store ResultStore) (Result, error) {
			return captureResult(cfg.Capture(ctx, target, store))
		},
	}
}

// CaptureGlobalBaselineCheck is the ScopeGlobal counterpart of
// CaptureBaselineCheck, for targets with no per-resource dimension.
func CaptureGlobalBaselineCheck(id CheckID, name string, capture GlobalCapture) Check {
	return NewCaptureGlobalBaselineCheck(CaptureGlobalBaselineCheckConfig{ID: id, Name: name, Capture: capture})
}
