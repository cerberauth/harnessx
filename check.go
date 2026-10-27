package harnessx

import (
	"context"
	"fmt"
	"time"
)

// SkipDecision decides at run time whether a check (or one of its resources)
// is skipped. The zero value never skips. Build one with SkipAlways, SkipWhen
// or SkipResourceWhen.
type SkipDecision struct {
	fn         func(ctx context.Context, target Target, store ResultStore) string
	resourceFn func(ctx context.Context, target Target, resource Resource, store ResultStore) string
}

// SkipAlways returns a SkipDecision that always skips the check with reason.
func SkipAlways(reason string) SkipDecision {
	return SkipDecision{fn: func(_ context.Context, _ Target, _ ResultStore) string {
		return reason
	}}
}

// SkipWhen builds a skip decision evaluated once per check, before it runs.
// fn returns a non-empty reason to skip the check, or "" to run it. It sees the
// results of the checks in earlier dependency levels.
func SkipWhen(fn func(ctx context.Context, target Target, store ResultStore) string) SkipDecision {
	return SkipDecision{fn: fn}
}

// SkipResourceWhen builds a skip decision evaluated once per resource for a
// ScopePerResource check, instead of once for the whole check. Use this when
// the decision depends on the resource itself (e.g. its security scheme)
// rather than on the target or environment.
func SkipResourceWhen(fn func(ctx context.Context, target Target, resource Resource, store ResultStore) string) SkipDecision {
	return SkipDecision{resourceFn: fn}
}

// Eval runs the skip decision and returns a non-empty skip reason if the
// check should be skipped, or "" if it should run.
func (s SkipDecision) Eval(ctx context.Context, target Target, store ResultStore) string {
	if s.fn == nil {
		return ""
	}
	return s.fn(ctx, target, store)
}

// EvalResource runs the per-resource skip decision, falling back to Eval
// (the check-wide decision) when no resource-specific decision was set.
func (s SkipDecision) EvalResource(ctx context.Context, target Target, resource Resource, store ResultStore) string {
	if s.resourceFn != nil {
		return s.resourceFn(ctx, target, resource, store)
	}
	return s.Eval(ctx, target, store)
}

// CheckID uniquely identifies a check within an Engine or Scenario.
type CheckID string

// CheckScope selects how often a check runs: once for the whole target
// (ScopeGlobal) or once per discovered resource (ScopePerResource).
// The zero value is ScopeGlobal.
type CheckScope int

const (
	// ScopeGlobal checks run once, through Check.Run (or Check.RunVariant).
	// They may discover resources via Result.Resources.
	ScopeGlobal CheckScope = iota
	// ScopePerResource checks run once for every resource known when their
	// dependency level starts, through Check.RunResource (or
	// Check.RunResourceVariant). Resources come from earlier levels, so a
	// per-resource check needs a DependsOn on the check that discovers them;
	// with no resources available the check is skipped.
	ScopePerResource
)

// Observation is an issue or noteworthy outcome reported by a check. The engine
// does not fill in CheckID or ResourceID; for a check with Variants, Variant is
// set to the variant that produced the observation when left empty.
type Observation struct {
	CheckID     CheckID
	ResourceID  string
	Variant     string
	Title       string
	Description string
	Evidence    string
	Metadata    map[string]string
}

// Result is the outcome of one check execution (one resource, for a
// ScopePerResource check). The engine sets CheckID, ResourceID and Duration on
// the results it records, so a check function does not need to.
//
// A non-nil Err marks the check as failed; Observations report findings and do
// not by themselves fail it. Data carries a check's output to later checks,
// which read it with GetData or DataAs.
type Result struct {
	CheckID      CheckID
	ResourceID   string
	Observations []Observation
	Resources    []Resource
	Skipped      bool
	SkipReason   string
	Duration     time.Duration
	Metadata     map[string]string
	Data         any
	Err          error
	Attempts     []Attempt
}

// ResultStatus classifies a Result; see Result.Status.
type ResultStatus string

const (
	// StatusPassed: the check ran, returned no error and made no observations.
	StatusPassed ResultStatus = "passed"
	// StatusFailed: the check ran without error but made observations.
	StatusFailed ResultStatus = "failed"
	// StatusSkipped: the check, or the resource, was skipped.
	StatusSkipped ResultStatus = "skipped"
	// StatusError: the check returned an error or panicked.
	StatusError ResultStatus = "error"
)

// Status classifies r. An error wins over everything else, so a failed check is
// never reported as skipped or passed; then skipped; then observations.
func (r Result) Status() ResultStatus {
	switch {
	case r.Err != nil:
		return StatusError
	case r.Skipped:
		return StatusSkipped
	case len(r.Observations) > 0:
		return StatusFailed
	default:
		return StatusPassed
	}
}

// Attempt is the outcome of one variant of a check with Variants, recorded in
// Result.Attempts in the order of Check.Variants.
type Attempt struct {
	Variant      string
	Observations []Observation
	Resources    []Resource
	Duration     time.Duration
	Data         any
	Err          error
}

// ResultStore is a read-only view of the results recorded so far. A check's
// Skip decision, Conditions and run function receive a snapshot taken when
// their dependency level started, so they see the results of earlier levels
// only — never those of checks in the same level.
//
// A ScopePerResource check is recorded once per resource, readable with
// GetForResource. Get returns a rolled-up check-level Result for it, available
// once every resource has run: its Observations are all the resources'
// observations, its Err is the first resource error, and it is Skipped only if
// every resource was skipped. Data and Resources are not aggregated; read them
// per resource.
type ResultStore interface {
	// Get returns the result recorded for a check, and whether one exists. For
	// a ScopePerResource check it is the aggregate described above.
	Get(id CheckID) (Result, bool)
	// GetForResource returns the result of a ScopePerResource check for one
	// resource, and whether one exists.
	GetForResource(id CheckID, resourceID string) (Result, bool)
	// Observations returns every observation recorded so far, in no
	// particular order. Each is returned once, whether it comes from a global
	// or a per-resource result.
	Observations() []Observation
	// Resources returns every resource discovered so far.
	Resources() []Resource
}

// CheckFunc is the run function of a ScopeGlobal check.
type CheckFunc func(ctx context.Context, target Target, store ResultStore) (Result, error)

// ResourceCheckFunc is the run function of a ScopePerResource check; it is
// called once per resource.
type ResourceCheckFunc func(ctx context.Context, target Target, resource Resource, store ResultStore) (Result, error)

// VariantCheckFunc is the run function of a ScopeGlobal check with Variants; it
// is called once per variant.
type VariantCheckFunc func(ctx context.Context, target Target, variant string, store ResultStore) (Result, error)

// VariantResourceCheckFunc is the run function of a ScopePerResource check with
// Variants; it is called once per resource and variant.
type VariantResourceCheckFunc func(ctx context.Context, target Target, resource Resource, variant string, store ResultStore) (Result, error)

// Condition reports whether a check should run, given the results of earlier
// dependency levels. See IfCheckPassed, IfCheckObserved, IfCheckSkipped, All,
// Any and Not.
type Condition func(store ResultStore) bool

// VariantMode selects how the variants of a check are scheduled.
type VariantMode int

const (
	// VariantsSequential runs the variants one at a time, in order (the default).
	VariantsSequential VariantMode = iota
	// VariantsParallel runs all variants concurrently, without any limit on
	// their number.
	VariantsParallel
)

// Check is a unit of work in the dependency graph. Set Scope and the matching
// run function: Run for ScopeGlobal, RunResource for ScopePerResource — or,
// when Variants is non-empty, RunVariant / RunResourceVariant instead.
//
// Timeout bounds each invocation of the run function (each resource, for a
// per-resource check) and, for a check with Variants, the whole variant set;
// set VariantTimeout to bound each variant too. It defaults to the engine's default timeout (see
// WithDefaultTimeout). Concurrency overrides the engine's per-resource
// concurrency for this check.
//
// A check holds a slot of the engine's level-wide concurrency limit
// (WithMaxConcurrency) for as long as it runs, including while a per-resource
// check fans out over its resources, so up to WithMaxConcurrency ×
// WithMaxResourceConcurrency resource invocations can be in flight at once.
type Check struct {
	ID          CheckID
	Name        string
	Description string
	Link        string
	Tags        []string
	DependsOn   []CheckID
	Conditions  []Condition

	// Security metadata, mirrored from checkdef.CheckDef. Purely
	// descriptive — the engine only reads these for run-time check
	// selection (see WithMinCVSSScore, WithCWEID, WithOWASP, ...).
	CVSSVector string
	CVSSScore  float64
	CWEID      string
	CAPECID    string
	OWASP      string

	Skip SkipDecision

	Scope       CheckScope
	Run         CheckFunc
	RunResource ResourceCheckFunc

	Variants           []string
	VariantMode        VariantMode
	RunVariant         VariantCheckFunc
	RunResourceVariant VariantResourceCheckFunc

	// VariantTimeout, when positive, bounds each variant on its own; Timeout
	// still bounds the variant set as a whole. VariantConcurrency, when
	// positive, caps how many variants run at once in VariantsParallel mode.
	VariantTimeout     time.Duration
	VariantConcurrency int

	Timeout     time.Duration
	Concurrency int
}

// skipReason returns why c should not run against store, or "" if it should:
// its Skip decision first, then its Conditions.
func (c Check) skipReason(ctx context.Context, target Target, store ResultStore) string {
	if reason := c.Skip.Eval(ctx, target, store); reason != "" {
		return reason
	}
	for _, cond := range c.Conditions {
		if !cond(store) {
			return "condition not met"
		}
	}
	return ""
}

// validate reports a configuration error in c that would stop it from running:
// an unknown Scope, a per-resource skip decision on a global check, or a
// missing run function for its Scope and Variants.
func (c Check) validate() error {
	if c.Scope != ScopeGlobal && c.Scope != ScopePerResource {
		return fmt.Errorf("harnessx: check %q has invalid Scope %d", c.ID, c.Scope)
	}
	if c.Scope == ScopeGlobal && c.Skip.resourceFn != nil {
		return fmt.Errorf("harnessx: check %q has ScopeGlobal but a per-resource Skip decision (SkipResourceWhen); use SkipWhen", c.ID)
	}

	var run, field string
	switch {
	case c.Scope == ScopeGlobal && len(c.Variants) > 0:
		run, field = "RunVariant", "ScopeGlobal with Variants"
		if c.RunVariant != nil {
			return nil
		}
	case c.Scope == ScopeGlobal:
		run, field = "Run", "ScopeGlobal"
		if c.Run != nil {
			return nil
		}
	case len(c.Variants) > 0:
		run, field = "RunResourceVariant", "ScopePerResource with Variants"
		if c.RunResourceVariant != nil {
			return nil
		}
	default:
		run, field = "RunResource", "ScopePerResource"
		if c.RunResource != nil {
			return nil
		}
	}
	return fmt.Errorf("harnessx: check %q has %s but no %s set", c.ID, field, run)
}

func (c Check) variantLimits() variantLimits {
	return variantLimits{concurrency: c.VariantConcurrency, timeout: c.VariantTimeout}
}

// invoke returns the function that executes c once: for res, or for the whole
// target when res is nil. It picks between the plain and the variant run
// functions, so callers need not branch on Variants.
func (c Check) invoke(target Target, store ResultStore, res *Resource) func(context.Context) (Result, error) {
	switch {
	case res == nil && len(c.Variants) == 0:
		return func(ctx context.Context) (Result, error) { return c.Run(ctx, target, store) }
	case res == nil:
		return func(ctx context.Context) (Result, error) {
			return runVariants(ctx, c.Variants, c.VariantMode, c.variantLimits(), func(vctx context.Context, variant string) (Result, error) {
				return c.RunVariant(vctx, target, variant, store)
			}), nil
		}
	case len(c.Variants) == 0:
		return func(ctx context.Context) (Result, error) { return c.RunResource(ctx, target, *res, store) }
	default:
		return func(ctx context.Context) (Result, error) {
			return runVariants(ctx, c.Variants, c.VariantMode, c.variantLimits(), func(vctx context.Context, variant string) (Result, error) {
				return c.RunResourceVariant(vctx, target, *res, variant, store)
			}), nil
		}
	}
}
