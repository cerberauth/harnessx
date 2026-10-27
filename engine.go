package harnessx

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
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

// Observations returns every observation, ordered by check ID and then resource
// ID so that the result is stable between calls.
func (s *storeSnapshot) Observations() []Observation {
	ids := make([]CheckID, 0, len(s.results))
	for id := range s.results {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	keys := make([]checkResourceKey, 0, len(s.perRes))
	for k := range s.perRes {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b checkResourceKey) int {
		if c := strings.Compare(string(a.checkID), string(b.checkID)); c != 0 {
			return c
		}
		return strings.Compare(a.resourceID, b.resourceID)
	})

	var out []Observation
	for _, id := range ids {
		out = append(out, s.results[id].Observations...)
	}
	for _, k := range keys {
		out = append(out, s.perRes[k].Observations...)
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
// is already registered or repeated in checks, it returns ErrDuplicateCheckID
// wrapped with the ID; if a check cannot run (an unknown Scope, or no run
// function for its Scope and Variants), it returns an error naming it. Either
// way none of them is registered. Unknown dependencies and cycles span several
// checks and are reported by Run. Register is safe to call concurrently with
// Run.
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
		if err := c.validate(); err != nil {
			return err
		}
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
// New; ErrUnknownCheckID if WithOnly names an ID that matches no check; an error
// for a check with an invalid Scope or a missing run function; or the context's
// error if ctx is cancelled, in which case every check that did not get to run
// is recorded as skipped ("scan cancelled"). A failing or panicking check does
// not stop the scan: it is counted in ScanSummary.Failed and its error is on its
// Result. Reporter.OnScanComplete is called in every case.
func (e *Engine) Run(ctx context.Context, target Target, opts ...RunOption) (ScanSummary, error) {
	e.mu.RLock()
	checks := make([]Check, len(e.checks))
	copy(checks, e.checks)
	e.mu.RUnlock()

	cfg := newRunConfig(opts)
	preErr := e.initErr
	if preErr == nil {
		preErr = cfg.unknownOnly(checks)
	}
	return e.run(ctx, target, cfg.filter(checks), nil, preErr)
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
	cfg := newRunConfig(opts)
	return e.run(ctx, target, cfg.filter(checks), scenario.Reporters, cfg.unknownOnly(checks))
}

// scan is the state of one Run or RunScenario call.
type scan struct {
	cfg       engineConfig
	target    Target
	reporters []Reporter
	store     *resultStore
	levelSem  *semaphore.Semaphore

	// order is the dependency level of each check, used to sort the summary.
	order map[CheckID]int

	mu      sync.Mutex // guards summary
	summary ScanSummary
}

// validateChecks rejects duplicate IDs and checks that cannot run.
func validateChecks(checks []Check) error {
	seen := make(map[CheckID]struct{}, len(checks))
	for _, c := range checks {
		if _, dup := seen[c.ID]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateCheckID, c.ID)
		}
		seen[c.ID] = struct{}{}
		if err := c.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) run(ctx context.Context, target Target, checks []Check, callReporters []Reporter, preErr error) (ScanSummary, error) {
	start := time.Now()

	s := &scan{
		cfg:       e.cfg,
		target:    target,
		reporters: e.cfg.reporters,
		store:     newResultStore(),
		levelSem:  semaphore.New(e.cfg.maxConcurrency),
		summary:   ScanSummary{Target: target},
	}
	if len(callReporters) > 0 {
		s.reporters = callReporters
	}

	finalize := func(err error) (ScanSummary, error) {
		s.sortSummary()
		s.summary.Duration = time.Since(start)
		s.summary.Err = err
		for _, r := range s.reporters {
			r.OnScanComplete(s.summary)
		}
		return s.summary, err
	}

	if preErr != nil {
		return finalize(preErr)
	}
	if len(checks) == 0 {
		return finalize(ErrNoChecks)
	}
	if err := validateChecks(checks); err != nil {
		return finalize(err)
	}
	ordered, err := levels(checks)
	if err != nil {
		return finalize(err)
	}

	s.order = make(map[CheckID]int, len(checks))
	for i, level := range ordered {
		for _, c := range level {
			s.order[c.ID] = i
		}
	}

	s.summary.TotalChecks = len(checks)
	for _, r := range s.reporters {
		r.OnScanStart(target, s.summary.TotalChecks)
	}

	for i, level := range ordered {
		if ctx.Err() != nil {
			// Record every check that did not get to run, so the counts add up.
			for _, rest := range ordered[i:] {
				for _, chk := range rest {
					s.recordCancelled(chk)
				}
			}
			return finalize(ctx.Err())
		}
		s.runLevel(ctx, level)
	}

	if ctx.Err() != nil {
		return finalize(ctx.Err())
	}
	return finalize(nil)
}

// runLevel runs the checks of one dependency level concurrently, bounded by
// WithMaxConcurrency, against a snapshot of the results of the earlier levels.
func (s *scan) runLevel(ctx context.Context, level []Check) {
	snapshot := s.store.snapshot()

	var wg sync.WaitGroup
	for _, chk := range level {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !s.levelSem.Acquire(ctx.Done()) {
				s.recordCancelled(chk)
				return
			}
			defer s.levelSem.Release()
			s.runCheck(ctx, chk, snapshot)
		}()
	}
	wg.Wait()
}

func (s *scan) runCheck(ctx context.Context, chk Check, snapshot ResultStore) {
	if reason := chk.skipReason(ctx, s.target, snapshot); reason != "" {
		skipped := Result{CheckID: chk.ID, Skipped: true, SkipReason: reason}
		s.store.set(chk.ID, skipped)
		s.record(skipped)
		return
	}

	if chk.Scope == ScopeGlobal {
		s.runGlobal(ctx, chk, snapshot)
		return
	}
	s.runPerResource(ctx, chk, snapshot)
}

func (s *scan) runGlobal(ctx context.Context, chk Check, snapshot ResultStore) {
	for _, r := range s.reporters {
		r.OnCheckStart(chk, s.target, nil)
	}
	result := s.execute(ctx, chk, nil, snapshot)
	s.store.set(chk.ID, result)
	s.store.mergeResources(result.Resources)
	s.record(result)
}

func (s *scan) runPerResource(ctx context.Context, chk Check, snapshot ResultStore) {
	resources := snapshot.Resources()
	if len(resources) == 0 {
		reason := "no resources discovered"
		if len(chk.DependsOn) == 0 {
			reason += ": a ScopePerResource check needs DependsOn on the check that discovers its resources"
		}
		skipped := Result{CheckID: chk.ID, Skipped: true, SkipReason: reason}
		s.store.set(chk.ID, skipped)
		s.record(skipped)
		return
	}

	for _, r := range s.reporters {
		if pr, ok := r.(ResourcePlanReporter); ok {
			pr.OnResourcesDiscovered(chk, len(resources))
		}
	}

	resConcurrency := chk.Concurrency
	if resConcurrency <= 0 {
		resConcurrency = s.cfg.maxResourceConcurrency
	}
	resSem := semaphore.New(resConcurrency)

	fanStart := time.Now()
	results := make([]Result, len(resources))
	ran := make([]bool, len(resources))

	var wg sync.WaitGroup
	for i, res := range resources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !resSem.Acquire(ctx.Done()) {
				result := Result{CheckID: chk.ID, ResourceID: res.ID, Skipped: true, SkipReason: reasonCancelled}
				s.store.setForResource(chk.ID, res.ID, result)
				results[i], ran[i] = result, true
				s.record(result)
				return
			}
			defer resSem.Release()

			var result Result
			if reason := chk.Skip.EvalResource(ctx, s.target, res, snapshot); reason != "" {
				result = Result{CheckID: chk.ID, ResourceID: res.ID, Skipped: true, SkipReason: reason}
			} else {
				for _, r := range s.reporters {
					r.OnCheckStart(chk, s.target, &res)
				}
				result = s.execute(ctx, chk, &res, snapshot)
				s.store.mergeResources(result.Resources)
			}
			s.store.setForResource(chk.ID, res.ID, result)
			results[i], ran[i] = result, true
			s.record(result)
		}()
	}
	wg.Wait()
	s.store.setAggregate(chk.ID, aggregateResources(chk.ID, results, ran, time.Since(fanStart)))
}

const reasonCancelled = "scan cancelled"

// recordCancelled records chk as skipped because the scan was cancelled before
// it could run, so that Executed + Skipped + Failed still accounts for it.
func (s *scan) recordCancelled(chk Check) {
	skipped := Result{CheckID: chk.ID, Skipped: true, SkipReason: reasonCancelled}
	s.store.set(chk.ID, skipped)
	s.record(skipped)
}

// sortSummary puts Results and Observations in a stable order — dependency
// level, then check ID, then resource ID — instead of completion order.
func (s *scan) sortSummary() {
	s.mu.Lock()
	defer s.mu.Unlock()

	sort.SliceStable(s.summary.Results, func(i, j int) bool {
		a, b := s.summary.Results[i], s.summary.Results[j]
		if la, lb := s.order[a.CheckID], s.order[b.CheckID]; la != lb {
			return la < lb
		}
		if a.CheckID != b.CheckID {
			return a.CheckID < b.CheckID
		}
		return a.ResourceID < b.ResourceID
	})

	s.summary.Observations = nil
	for _, r := range s.summary.Results {
		s.summary.Observations = append(s.summary.Observations, r.Observations...)
	}
}

// execute runs chk once, for res or, when res is nil, for the whole target. It
// applies the check's timeout (or the engine default) and recovers panics.
func (s *scan) execute(ctx context.Context, chk Check, res *Resource, snapshot ResultStore) Result {
	timeout := chk.Timeout
	if timeout == 0 {
		timeout = s.cfg.defaultTimeout
	}
	result := runSafe(ctx, chk.ID, timeout, chk.invoke(s.target, snapshot, res))
	result.CheckID = chk.ID
	if res != nil {
		result.ResourceID = res.ID
	}
	return result
}

// record reports a finished result to the reporters and adds it to the summary.
func (s *scan) record(result Result) {
	for _, r := range s.reporters {
		r.OnCheckComplete(result)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch result.Status() {
	case StatusError:
		s.summary.Failed++
	case StatusSkipped:
		s.summary.Skipped++
	default:
		s.summary.Executed++
	}
	s.summary.Observations = append(s.summary.Observations, result.Observations...)
	s.summary.Results = append(s.summary.Results, result)
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
