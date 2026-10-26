package harnessx

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/cerberauth/harnessx/internal/semaphore"
)

type resultStore struct {
	mu      sync.RWMutex
	results map[CheckID]Result
	perRes  map[checkResourceKey]Result
	// aggregates holds, for each ScopePerResource check, a check-level Result
	// rolled up from its per-resource results. It is served by Get but kept out
	// of Observations so findings are not counted twice.
	aggregates map[CheckID]Result
	resources  []Resource
	resourceID map[string]struct{}
}

type checkResourceKey struct {
	checkID    CheckID
	resourceID string
}

func newResultStore() *resultStore {
	return &resultStore{
		results:    make(map[CheckID]Result),
		perRes:     make(map[checkResourceKey]Result),
		aggregates: make(map[CheckID]Result),
		resourceID: make(map[string]struct{}),
	}
}

func (s *resultStore) set(id CheckID, r Result) {
	s.mu.Lock()
	s.results[id] = r
	s.mu.Unlock()
}

func (s *resultStore) setForResource(id CheckID, resourceID string, r Result) {
	s.mu.Lock()
	s.perRes[checkResourceKey{id, resourceID}] = r
	s.mu.Unlock()
}

func (s *resultStore) setAggregate(id CheckID, r Result) {
	s.mu.Lock()
	s.aggregates[id] = r
	s.mu.Unlock()
}

// mergeResources adds resources to the store, dropping any whose ID is already
// known: the first resource discovered under an ID wins, so a resource that
// two checks both emit is run against once.
func (s *resultStore) mergeResources(resources []Resource) {
	if len(resources) == 0 {
		return
	}
	s.mu.Lock()
	for _, res := range resources {
		if _, dup := s.resourceID[res.ID]; dup {
			continue
		}
		s.resourceID[res.ID] = struct{}{}
		s.resources = append(s.resources, res)
	}
	s.mu.Unlock()
}

func (s *resultStore) snapshot() ResultStore {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make(map[CheckID]Result, len(s.results))
	for k, v := range s.results {
		results[k] = v
	}
	perRes := make(map[checkResourceKey]Result, len(s.perRes))
	for k, v := range s.perRes {
		perRes[k] = v
	}
	aggregates := make(map[CheckID]Result, len(s.aggregates))
	for k, v := range s.aggregates {
		aggregates[k] = v
	}
	resources := make([]Resource, len(s.resources))
	copy(resources, s.resources)

	return &storeSnapshot{results: results, perRes: perRes, aggregates: aggregates, resources: resources}
}

type storeSnapshot struct {
	results    map[CheckID]Result
	perRes     map[checkResourceKey]Result
	aggregates map[CheckID]Result
	resources  []Resource
}

func (s *storeSnapshot) Get(id CheckID) (Result, bool) {
	if r, ok := s.results[id]; ok {
		return r, true
	}
	r, ok := s.aggregates[id]
	return r, ok
}

func (s *storeSnapshot) GetForResource(id CheckID, resourceID string) (Result, bool) {
	r, ok := s.perRes[checkResourceKey{id, resourceID}]
	return r, ok
}

func (s *storeSnapshot) Observations() []Observation {
	var out []Observation
	for _, r := range s.results {
		out = append(out, r.Observations...)
	}
	for _, r := range s.perRes {
		out = append(out, r.Observations...)
	}
	return out
}

func (s *storeSnapshot) Resources() []Resource {
	return s.resources
}

// Engine is safe for concurrent Run/RunScenario calls, including a
// RunScenario issued reentrantly from inside another check's
// Run/RunResource callback on the same engine: each call gets its own
// resultStore and semaphores, e.checks is only read (not written) outside
// Register, and e.cfg is immutable after New().
type Engine struct {
	cfg    engineConfig
	checks []Check
	mu     sync.RWMutex

	// initErr is the error from registering the WithChecks checks in New, which
	// has no way to return it; Run reports it instead of running a partial set.
	initErr error
}

// New returns an Engine configured by opts. By default it runs as many checks,
// and as many resources per check, concurrently as there are CPUs, with a 30
// second check timeout and no reporters.
//
// Checks passed with WithChecks are registered immediately. New cannot return
// a registration error such as a duplicate check ID, so Run returns it instead
// and runs nothing; use Register to see it right away.
func New(opts ...Option) *Engine {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	e := &Engine{cfg: cfg}
	if len(cfg.initialChecks) > 0 {
		e.initErr = e.Register(cfg.initialChecks...)
	}
	return e
}

// Register adds checks to the engine. It is all or nothing: if any check's ID
// is already registered, or repeated in checks, it returns ErrDuplicateCheckID
// wrapped with the ID and registers none of them. Unknown dependencies and
// cycles are reported by Run, not here. Register is safe to call concurrently
// with Run.
func (e *Engine) Register(checks ...Check) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	seen := make(map[CheckID]struct{}, len(e.checks)+len(checks))
	for _, c := range e.checks {
		seen[c.ID] = struct{}{}
	}
	for _, c := range checks {
		if _, dup := seen[c.ID]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateCheckID, c.ID)
		}
		seen[c.ID] = struct{}{}
	}
	e.checks = append(e.checks, checks...)
	return nil
}

// Run executes the registered checks against target and returns a summary.
//
// Checks are grouped into levels by their DependsOn graph; the checks of a
// level run concurrently, bounded by WithMaxConcurrency, and a level starts
// once the previous one is done. opts narrow the registered checks for this
// call only; see WithOnly, WithExclude and the metadata filters.
//
// Run returns ErrNoChecks if no check is left to run; ErrDuplicateCheckID,
// ErrUnknownDependency or ErrCycleDetected (wrapped with the offending ID) for
// an invalid graph, including a duplicate among the WithChecks checks passed to
// New; an error for a check with an invalid Scope or a missing run function;
// or the context's error if ctx is cancelled. A failing or panicking check does
// not stop the scan: it is counted in ScanSummary.Failed and its error is on its
// Result. Reporter.OnScanComplete is called in every case.
func (e *Engine) Run(ctx context.Context, target Target, opts ...RunOption) (ScanSummary, error) {
	e.mu.RLock()
	checks := make([]Check, len(e.checks))
	copy(checks, e.checks)
	e.mu.RUnlock()

	return e.run(ctx, target, FilterChecks(checks, opts...), nil, e.initErr)
}

// RunScenario executes the checks in scenario against target using the
// engine's concurrency limits and default timeout. It does not consult or
// modify the engine's registered check list — scenario.Checks is fully
// self-contained.
//
// opts narrow scenario.Checks exactly as they narrow the registered checks in
// Run, including the transitive dependency closure: a kept check's
// dependencies still run unless excluded.
//
// If scenario.Reporters is non-empty it is used instead of the engine's
// configured reporters (see [WithReporters]), so a shared engine can give
// each caller its own reporters.
//
// Reporter.OnScanComplete is always called before returning, even if
// scenario.Checks is empty.
func (e *Engine) RunScenario(ctx context.Context, target Target, scenario Scenario, opts ...RunOption) (ScanSummary, error) {
	checks := make([]Check, len(scenario.Checks))
	copy(checks, scenario.Checks)
	return e.run(ctx, target, FilterChecks(checks, opts...), scenario.Reporters, nil)
}

func (e *Engine) run(ctx context.Context, target Target, checks []Check, callReporters []Reporter, preErr error) (ScanSummary, error) {
	start := time.Now()

	cfg := e.cfg
	reporters := cfg.reporters
	if len(callReporters) > 0 {
		reporters = callReporters
	}

	summary := ScanSummary{Target: target}

	finalize := func(err error) (ScanSummary, error) {
		summary.Duration = time.Since(start)
		summary.Err = err
		for _, r := range reporters {
			r.OnScanComplete(summary)
		}
		return summary, err
	}

	if preErr != nil {
		return finalize(preErr)
	}
	if len(checks) == 0 {
		return finalize(ErrNoChecks)
	}

	seen := make(map[CheckID]struct{}, len(checks))
	for _, c := range checks {
		if _, dup := seen[c.ID]; dup {
			return finalize(fmt.Errorf("%w: %q", ErrDuplicateCheckID, c.ID))
		}
		seen[c.ID] = struct{}{}
		if c.Scope != ScopeGlobal && c.Scope != ScopePerResource {
			return finalize(fmt.Errorf("harnessx: check %q has invalid Scope %d", c.ID, c.Scope))
		}
		if c.Scope == ScopeGlobal && c.Skip.resourceFn != nil {
			return finalize(fmt.Errorf("harnessx: check %q has ScopeGlobal but a per-resource Skip decision (SkipResourceWhen); use SkipWhen", c.ID))
		}
		if len(c.Variants) > 0 {
			if c.Scope == ScopePerResource && c.RunResourceVariant == nil {
				return finalize(fmt.Errorf("harnessx: check %q has ScopePerResource with Variants but no RunResourceVariant set", c.ID))
			}
			if c.Scope == ScopeGlobal && c.RunVariant == nil {
				return finalize(fmt.Errorf("harnessx: check %q has ScopeGlobal with Variants but no RunVariant set", c.ID))
			}
		} else {
			if c.Scope == ScopePerResource && c.RunResource == nil {
				return finalize(fmt.Errorf("harnessx: check %q has ScopePerResource but no RunResource set", c.ID))
			}
			if c.Scope == ScopeGlobal && c.Run == nil {
				return finalize(fmt.Errorf("harnessx: check %q has ScopeGlobal but no Run set", c.ID))
			}
		}
	}

	g, err := newGraph(checks)
	if err != nil {
		return finalize(err)
	}
	levels, err := g.topoSort()
	if err != nil {
		return finalize(err)
	}

	summary.TotalChecks = len(checks)
	for _, r := range reporters {
		r.OnScanStart(target, summary.TotalChecks)
	}

	store := newResultStore()
	levelSem := semaphore.New(cfg.maxConcurrency)

	for _, level := range levels {
		if ctx.Err() != nil {
			return finalize(ctx.Err())
		}

		snapshot := store.snapshot()

		var levelWg sync.WaitGroup
		var summaryMu sync.Mutex

		for _, chk := range level {
			levelWg.Add(1)
			go func() {
				defer levelWg.Done()
				if !levelSem.Acquire(ctx.Done()) {
					return
				}
				defer levelSem.Release()

				if reason := chk.Skip.Eval(ctx, target, snapshot); reason != "" {
					skipped := Result{CheckID: chk.ID, Skipped: true, SkipReason: reason}
					store.set(chk.ID, skipped)
					for _, r := range reporters {
						r.OnCheckComplete(skipped)
					}
					summaryMu.Lock()
					summary.Skipped++
					summary.Results = append(summary.Results, skipped)
					summaryMu.Unlock()
					return
				}

				for _, cond := range chk.Conditions {
					if !cond(snapshot) {
						skipped := Result{CheckID: chk.ID, Skipped: true, SkipReason: "condition not met"}
						store.set(chk.ID, skipped)
						for _, r := range reporters {
							r.OnCheckComplete(skipped)
						}
						summaryMu.Lock()
						summary.Skipped++
						summary.Results = append(summary.Results, skipped)
						summaryMu.Unlock()
						return
					}
				}

				effectiveTimeout := chk.Timeout
				if effectiveTimeout == 0 {
					effectiveTimeout = cfg.defaultTimeout
				}

				if chk.Scope == ScopeGlobal {
					for _, r := range reporters {
						r.OnCheckStart(chk, target, nil)
					}
					var result Result
					if len(chk.Variants) > 0 {
						result = runSafe(ctx, chk.ID, effectiveTimeout, func(checkCtx context.Context) (Result, error) {
							return RunVariants(checkCtx, chk.Variants, chk.VariantMode, func(vctx context.Context, variant string) (Result, error) {
								return chk.RunVariant(vctx, target, variant, snapshot)
							}), nil
						})
					} else {
						result = runSafe(ctx, chk.ID, effectiveTimeout, func(checkCtx context.Context) (Result, error) {
							return chk.Run(checkCtx, target, snapshot)
						})
					}
					result.CheckID = chk.ID
					for _, r := range reporters {
						r.OnCheckComplete(result)
					}
					store.set(chk.ID, result)
					store.mergeResources(result.Resources)

					summaryMu.Lock()
					switch {
					case result.Err != nil:
						summary.Failed++
					case result.Skipped:
						summary.Skipped++
					default:
						summary.Executed++
					}
					summary.Observations = append(summary.Observations, result.Observations...)
					summary.Results = append(summary.Results, result)
					summaryMu.Unlock()
					return
				}

				resources := snapshot.Resources()
				if len(resources) == 0 {
					skipped := Result{CheckID: chk.ID, Skipped: true, SkipReason: "no resources discovered"}
					store.set(chk.ID, skipped)
					for _, r := range reporters {
						r.OnCheckComplete(skipped)
					}
					summaryMu.Lock()
					summary.Skipped++
					summary.Results = append(summary.Results, skipped)
					summaryMu.Unlock()
					return
				}

				for _, r := range reporters {
					if pr, ok := r.(ResourcePlanReporter); ok {
						pr.OnResourcesDiscovered(chk, len(resources))
					}
				}

				resConcurrency := chk.Concurrency
				if resConcurrency <= 0 {
					resConcurrency = cfg.maxResourceConcurrency
				}
				resSem := semaphore.New(resConcurrency)

				fanStart := time.Now()
				resResults := make([]Result, len(resources))
				resRan := make([]bool, len(resources))

				var resWg sync.WaitGroup
				for i, res := range resources {
					resWg.Add(1)
					go func() {
						defer resWg.Done()
						if !resSem.Acquire(ctx.Done()) {
							return
						}
						defer resSem.Release()

						if reason := chk.Skip.EvalResource(ctx, target, res, snapshot); reason != "" {
							skipped := Result{CheckID: chk.ID, ResourceID: res.ID, Skipped: true, SkipReason: reason}
							store.setForResource(chk.ID, res.ID, skipped)
							resResults[i], resRan[i] = skipped, true
							for _, r := range reporters {
								r.OnCheckComplete(skipped)
							}
							summaryMu.Lock()
							summary.Skipped++
							summary.Results = append(summary.Results, skipped)
							summaryMu.Unlock()
							return
						}

						for _, r := range reporters {
							r.OnCheckStart(chk, target, &res)
						}
						var result Result
						if len(chk.Variants) > 0 {
							result = runSafe(ctx, chk.ID, effectiveTimeout, func(checkCtx context.Context) (Result, error) {
								return RunVariants(checkCtx, chk.Variants, chk.VariantMode, func(vctx context.Context, variant string) (Result, error) {
									return chk.RunResourceVariant(vctx, target, res, variant, snapshot)
								}), nil
							})
						} else {
							result = runSafe(ctx, chk.ID, effectiveTimeout, func(checkCtx context.Context) (Result, error) {
								return chk.RunResource(checkCtx, target, res, snapshot)
							})
						}
						result.CheckID = chk.ID
						result.ResourceID = res.ID
						for _, r := range reporters {
							r.OnCheckComplete(result)
						}
						store.setForResource(chk.ID, res.ID, result)
						resResults[i], resRan[i] = result, true
						store.mergeResources(result.Resources)

						summaryMu.Lock()
						switch {
						case result.Err != nil:
							summary.Failed++
						case result.Skipped:
							summary.Skipped++
						default:
							summary.Executed++
						}
						summary.Observations = append(summary.Observations, result.Observations...)
						summary.Results = append(summary.Results, result)
						summaryMu.Unlock()
					}()
				}
				resWg.Wait()
				store.setAggregate(chk.ID, aggregateResources(chk.ID, resResults, resRan, time.Since(fanStart)))
			}()
		}
		levelWg.Wait()
	}

	if ctx.Err() != nil {
		return finalize(ctx.Err())
	}
	return finalize(nil)
}

// aggregateResources rolls the per-resource results of one check up into the
// check-level Result served by ResultStore.Get. ran marks the resources that
// actually produced a result (none are missing unless the scan was cancelled).
//
// The aggregate holds every resource's observations, the first error in
// resource order, and is Skipped only when every resource was skipped. It is
// kept out of the scan summary, which counts the per-resource results.
func aggregateResources(id CheckID, results []Result, ran []bool, duration time.Duration) Result {
	agg := Result{CheckID: id, Duration: duration}
	completed, skipped := 0, 0
	for i, r := range results {
		if !ran[i] {
			continue
		}
		completed++
		agg.Observations = append(agg.Observations, r.Observations...)
		switch {
		case r.Err != nil:
			if agg.Err == nil {
				agg.Err = r.Err
			}
		case r.Skipped:
			skipped++
		}
	}
	if completed > 0 && skipped == completed {
		agg.Skipped = true
		agg.SkipReason = "all resources skipped"
	}
	return agg
}

// tagScanErrors sets CheckID on any recovered-panic ScanError in result that
// does not have one yet. RunVariants records variant panics without knowing the
// check they belong to.
func tagScanErrors(id CheckID, result *Result) {
	tag := func(err error) {
		var se *ScanError
		if errors.As(err, &se) && se.CheckID == "" {
			se.CheckID = id
		}
	}
	tag(result.Err)
	for i := range result.Attempts {
		tag(result.Attempts[i].Err)
	}
}

func runSafe(ctx context.Context, id CheckID, timeout time.Duration, fn func(context.Context) (Result, error)) (result Result) {
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	defer func() {
		result.Duration = time.Since(start)
		if r := recover(); r != nil {
			result.Err = &ScanError{CheckID: id, Cause: fmt.Errorf("panic: %v", r), Stack: debug.Stack()}
		}
	}()

	var err error
	result, err = fn(checkCtx)
	if err != nil && result.Err == nil {
		result.Err = err
	}
	tagScanErrors(id, &result)
	return result
}
