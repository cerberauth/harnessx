package harnessx

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cerberauth/harnessx/internal/semaphore"
)

type resultStore struct {
	mu        sync.RWMutex
	results   map[CheckID]Result
	perRes    map[checkResourceKey]Result
	resources []Resource
}

type checkResourceKey struct {
	checkID    CheckID
	resourceID string
}

func newResultStore() *resultStore {
	return &resultStore{
		results: make(map[CheckID]Result),
		perRes:  make(map[checkResourceKey]Result),
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

func (s *resultStore) mergeResources(resources []Resource) {
	if len(resources) == 0 {
		return
	}
	s.mu.Lock()
	s.resources = append(s.resources, resources...)
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
	resources := make([]Resource, len(s.resources))
	copy(resources, s.resources)

	return &storeSnapshot{results: results, perRes: perRes, resources: resources}
}

type storeSnapshot struct {
	results   map[CheckID]Result
	perRes    map[checkResourceKey]Result
	resources []Resource
}

func (s *storeSnapshot) Get(id CheckID) (Result, bool) {
	r, ok := s.results[id]
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

type Engine struct {
	cfg    engineConfig
	checks []Check
	mu     sync.RWMutex
}

func New(opts ...Option) *Engine {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	e := &Engine{cfg: cfg}
	if len(cfg.initialChecks) > 0 {
		_ = e.Register(cfg.initialChecks...)
	}
	return e
}

func (e *Engine) Register(checks ...Check) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	existing := make(map[CheckID]struct{}, len(e.checks))
	for _, c := range e.checks {
		existing[c.ID] = struct{}{}
	}
	for _, c := range checks {
		if _, dup := existing[c.ID]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateCheckID, c.ID)
		}
		existing[c.ID] = struct{}{}
		e.checks = append(e.checks, c)
	}
	return nil
}

func (e *Engine) Run(ctx context.Context, target Target, opts ...RunOption) (ScanSummary, error) {
	e.mu.RLock()
	checks := make([]Check, len(e.checks))
	copy(checks, e.checks)
	e.mu.RUnlock()

	var cfg runConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return e.run(ctx, target, cfg.filter(checks))
}

// RunScenario executes the checks in scenario against target using the engine's
// configured reporters, concurrency limits, and default timeout. It does not
// consult or modify the engine's registered check list — scenario.Checks is
// fully self-contained.
//
// Reporter.OnScanComplete is always called before returning, even if
// scenario.Checks is empty.
func (e *Engine) RunScenario(ctx context.Context, target Target, scenario Scenario) (ScanSummary, error) {
	checks := make([]Check, len(scenario.Checks))
	copy(checks, scenario.Checks)
	return e.run(ctx, target, checks)
}

func (e *Engine) run(ctx context.Context, target Target, checks []Check) (ScanSummary, error) {
	start := time.Now()

	cfg := e.cfg
	reporters := cfg.reporters

	summary := ScanSummary{Target: target}

	finalize := func(err error) (ScanSummary, error) {
		summary.Duration = time.Since(start)
		summary.Err = err
		for _, r := range reporters {
			r.OnScanComplete(summary)
		}
		return summary, err
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
						skipped := Result{CheckID: chk.ID, Skipped: true}
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
						result = runSafe(ctx, effectiveTimeout, func(checkCtx context.Context) (Result, error) {
							return runVariants(checkCtx, chk.Variants, chk.VariantMode, func(vctx context.Context, variant string) (Result, error) {
								return chk.RunVariant(vctx, target, variant, snapshot)
							}), nil
						})
					} else {
						result = runSafe(ctx, effectiveTimeout, func(checkCtx context.Context) (Result, error) {
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
					if result.Err != nil {
						summary.Failed++
					} else {
						summary.Executed++
					}
					summary.Observations = append(summary.Observations, result.Observations...)
					summary.Results = append(summary.Results, result)
					summaryMu.Unlock()
					return
				}

				resources := snapshot.Resources()
				if len(resources) == 0 {
					skipped := Result{CheckID: chk.ID, Skipped: true}
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

				resConcurrency := chk.Concurrency
				if resConcurrency <= 0 {
					resConcurrency = cfg.maxResourceConcurrency
				}
				resSem := semaphore.New(resConcurrency)

				var resWg sync.WaitGroup
				for _, res := range resources {
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
							result = runSafe(ctx, effectiveTimeout, func(checkCtx context.Context) (Result, error) {
								return runVariants(checkCtx, chk.Variants, chk.VariantMode, func(vctx context.Context, variant string) (Result, error) {
									return chk.RunResourceVariant(vctx, target, res, variant, snapshot)
								}), nil
							})
						} else {
							result = runSafe(ctx, effectiveTimeout, func(checkCtx context.Context) (Result, error) {
								return chk.RunResource(checkCtx, target, res, snapshot)
							})
						}
						result.CheckID = chk.ID
						result.ResourceID = res.ID
						for _, r := range reporters {
							r.OnCheckComplete(result)
						}
						store.setForResource(chk.ID, res.ID, result)
						store.mergeResources(result.Resources)

						summaryMu.Lock()
						if result.Err != nil {
							summary.Failed++
						} else {
							summary.Executed++
						}
						summary.Observations = append(summary.Observations, result.Observations...)
						summary.Results = append(summary.Results, result)
						summaryMu.Unlock()
					}()
				}
				resWg.Wait()
			}()
		}
		levelWg.Wait()
	}

	if ctx.Err() != nil {
		return finalize(ctx.Err())
	}
	return finalize(nil)
}

func runSafe(ctx context.Context, timeout time.Duration, fn func(context.Context) (Result, error)) (result Result) {
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	defer func() {
		result.Duration = time.Since(start)
		if r := recover(); r != nil {
			result.Err = &ScanError{Cause: fmt.Errorf("panic: %v", r)}
		}
	}()

	var err error
	result, err = fn(checkCtx)
	if err != nil && result.Err == nil {
		result.Err = err
	}
	return result
}
