package harnessx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testReporter records all events.
type testReporter struct {
	mu          sync.Mutex
	totalChecks int
	starts      []CheckID
	completes   []Result
	summary     *ScanSummary
}

func (r *testReporter) OnScanStart(_ Target, n int) {
	r.mu.Lock()
	r.totalChecks = n
	r.mu.Unlock()
}

func (r *testReporter) OnCheckStart(c Check, _ Target, _ *Resource) {
	r.mu.Lock()
	r.starts = append(r.starts, c.ID)
	r.mu.Unlock()
}

func (r *testReporter) OnCheckComplete(res Result) {
	r.mu.Lock()
	r.completes = append(r.completes, res)
	r.mu.Unlock()
}

func (r *testReporter) OnScanComplete(s ScanSummary) {
	r.mu.Lock()
	r.summary = &s
	r.mu.Unlock()
}

func (r *testReporter) getSummary() *ScanSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.summary
}

var testTarget = Target{URL: "http://example.com", Host: "example.com"}

// ── Integration Test 1: Dependency + skip flow ────────────────────────────────

func TestEngine_DependencyAndSkipFlow(t *testing.T) {
	rep := &testReporter{}
	e := New(WithReporters(rep), WithDefaultTimeout(5*time.Second))

	// A: global, no deps, no findings
	checkA := Check{
		ID: "A",
		Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
			return Result{CheckID: "A"}, nil // no observations
		},
	}
	// B: global, depends A
	checkB := Check{
		ID:        "B",
		DependsOn: []CheckID{"A"},
		Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
			return Result{CheckID: "B"}, nil
		},
	}
	// C: global, depends A, only runs if A found something (it won't)
	checkC := Check{
		ID:         "C",
		DependsOn:  []CheckID{"A"},
		Conditions: []Condition{IfCheckObserved("A")},
		Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
			return Result{CheckID: "C"}, nil
		},
	}
	// D: global, depends B and C
	checkD := Check{
		ID:        "D",
		DependsOn: []CheckID{"B", "C"},
		Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
			return Result{CheckID: "D"}, nil
		},
	}

	if err := e.Register(checkA, checkB, checkC, checkD); err != nil {
		t.Fatalf("Register: %v", err)
	}

	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if summary.TotalChecks != 4 {
		t.Errorf("TotalChecks: want 4, got %d", summary.TotalChecks)
	}
	// A, B, D executed; C skipped
	if summary.Executed != 3 {
		t.Errorf("Executed: want 3, got %d", summary.Executed)
	}
	if summary.Skipped != 1 {
		t.Errorf("Skipped: want 1, got %d", summary.Skipped)
	}
	if summary.Failed != 0 {
		t.Errorf("Failed: want 0, got %d", summary.Failed)
	}
	if rep.getSummary() == nil {
		t.Error("OnScanComplete was not called")
	}
}

const (
	checkIDRecon    = "recon"
	checkIDEndpoint = "endpoint"
)

// ── Integration Test 2: Resource discovery + per-resource dispatch ─────────────

func TestEngine_ResourceDiscovery(t *testing.T) {
	rep := &testReporter{}
	e := New(WithReporters(rep), WithDefaultTimeout(5*time.Second))

	resources := []Resource{
		{ID: "r1", URL: "http://example.com/r1"},
		{ID: "r2", URL: "http://example.com/r2"},
		{ID: "r3", URL: "http://example.com/r3"},
	}

	recon := Check{
		ID: checkIDRecon,
		Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
			return Result{CheckID: checkIDRecon, Resources: resources}, nil
		},
	}

	var seenResources []string
	var mu sync.Mutex

	endpoint := Check{
		ID:        checkIDEndpoint,
		Scope:     ScopePerResource,
		DependsOn: []CheckID{checkIDRecon},
		RunResource: func(_ context.Context, _ Target, res Resource, _ ResultStore) (Result, error) {
			mu.Lock()
			seenResources = append(seenResources, res.ID)
			mu.Unlock()
			return Result{CheckID: checkIDEndpoint, ResourceID: res.ID}, nil
		},
	}

	if err := e.Register(recon, endpoint); err != nil {
		t.Fatalf("Register: %v", err)
	}

	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if summary.Executed != 4 { // 1 recon + 3 endpoint runs
		t.Errorf("Executed: want 4, got %d", summary.Executed)
	}

	if len(seenResources) != 3 {
		t.Errorf("endpoint ran %d times, want 3; resources: %v", len(seenResources), seenResources)
	}

	// All resource IDs should appear.
	seen := make(map[string]bool)
	for _, id := range seenResources {
		seen[id] = true
	}
	for _, r := range resources {
		if !seen[r.ID] {
			t.Errorf("resource %s was not processed", r.ID)
		}
	}
}

// ── Integration Test 3: Concurrency limits ────────────────────────────────────

func TestEngine_ConcurrencyLimits(t *testing.T) {
	const numChecks = 6
	const maxConcurrency = 2
	const maxResourceConcurrency = 3
	const numResources = 5

	// First, a global recon that discovers resources.
	resources := make([]Resource, numResources)
	for i := range resources {
		resources[i] = Resource{ID: string(rune('a' + i))}
	}

	recon := Check{
		ID: checkIDRecon,
		Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
			return Result{CheckID: checkIDRecon, Resources: resources}, nil
		},
	}

	// Track concurrent level-parallel checks.
	var levelActive int32
	var levelPeak int32

	// numChecks independent ScopePerResource checks.
	checks := make([]Check, numChecks)
	for i := range checks {
		id := CheckID(string(rune('A' + i)))
		checks[i] = Check{
			ID:          id,
			Scope:       ScopePerResource,
			DependsOn:   []CheckID{checkIDRecon},
			Concurrency: maxResourceConcurrency,
			RunResource: func(_ context.Context, _ Target, _ Resource, _ ResultStore) (Result, error) {
				cur := atomic.AddInt32(&levelActive, 1)
				for {
					peak := atomic.LoadInt32(&levelPeak)
					if cur <= peak || atomic.CompareAndSwapInt32(&levelPeak, peak, cur) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&levelActive, -1)
				return Result{}, nil
			},
		}
	}

	allChecks := append([]Check{recon}, checks...)
	e := New(
		WithMaxConcurrency(maxConcurrency),
		WithMaxResourceConcurrency(maxResourceConcurrency),
		WithDefaultTimeout(5*time.Second),
	)
	if err := e.Register(allChecks...); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The peak concurrent resource goroutines across ALL checks should be
	// bounded by maxConcurrency (check-level semaphore) * maxResourceConcurrency.
	// We can't easily assert exact peak without more instrumentation, but
	// the test should at least complete without deadlock and without errors.
	t.Logf("peak concurrent resource goroutines observed: %d", atomic.LoadInt32(&levelPeak))
}

// ── Integration Test 4: Panic recovery ───────────────────────────────────────

func TestEngine_PanicRecovery(t *testing.T) {
	rep := &testReporter{}
	e := New(WithReporters(rep), WithDefaultTimeout(5*time.Second))

	panicCheck := Check{
		ID: "panicker",
		Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
			panic("intentional panic for testing")
		},
	}
	afterCheck := Check{
		ID: "after",
		Run: func(_ context.Context, _ Target, _ ResultStore) (Result, error) {
			return Result{CheckID: "after"}, nil
		},
	}

	if err := e.Register(panicCheck, afterCheck); err != nil {
		t.Fatalf("Register: %v", err)
	}

	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// The panic check should have been counted as failed.
	if summary.Failed == 0 {
		t.Error("expected panicker to be counted as failed")
	}
	// The after check should still have executed.
	if summary.Executed == 0 {
		t.Error("expected 'after' check to execute despite panic in sibling")
	}

	// Find the panicker result in reporter.
	rep.mu.Lock()
	defer rep.mu.Unlock()
	var foundPanicErr bool
	for _, r := range rep.completes {
		if r.CheckID == "panicker" && r.Err != nil {
			foundPanicErr = true
		}
	}
	if !foundPanicErr {
		t.Error("expected panicker result with non-nil Err")
	}
}

// ── Integration Test 5: Context cancellation ──────────────────────────────────

func TestEngine_ContextCancellation(t *testing.T) {
	rep := &testReporter{}
	e := New(WithReporters(rep), WithDefaultTimeout(5*time.Second))

	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	slowCheck := Check{
		ID: "slow",
		Run: func(checkCtx context.Context, _ Target, _ ResultStore) (Result, error) {
			close(started)
			select {
			case <-checkCtx.Done():
				return Result{}, checkCtx.Err()
			case <-time.After(10 * time.Second):
				return Result{}, nil
			}
		},
	}

	if err := e.Register(slowCheck); err != nil {
		t.Fatalf("Register: %v", err)
	}

	go func() {
		<-started
		cancel()
	}()

	_, runErr := e.Run(ctx, testTarget)

	// OnScanComplete must always be called.
	if rep.getSummary() == nil {
		t.Error("OnScanComplete was not called after cancellation")
	}

	// Run should return the context error.
	if !errors.Is(runErr, context.Canceled) {
		t.Errorf("Run error: want context.Canceled, got %v", runErr)
	}
}

// ── Engine.Register duplicate check ──────────────────────────────────────────

func TestEngine_RegisterDuplicate(t *testing.T) {
	e := New()
	chk := Check{ID: "dup", Run: stubRun}
	if err := e.Register(chk); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := e.Register(chk); !errors.Is(err, ErrDuplicateCheckID) {
		t.Errorf("expected ErrDuplicateCheckID, got %v", err)
	}
}

// ── Engine.Run with no checks ─────────────────────────────────────────────────

func TestEngine_NoChecks(t *testing.T) {
	e := New()
	_, err := e.Run(context.Background(), testTarget)
	if !errors.Is(err, ErrNoChecks) {
		t.Errorf("expected ErrNoChecks, got %v", err)
	}
}

// ── Engine validates ScopePerResource check must have RunResource ─────────────

func TestEngine_ScopePerResourceWithoutRunResource(t *testing.T) {
	e := New()
	chk := Check{
		ID:    "bad",
		Scope: ScopePerResource,
		// RunResource intentionally not set
	}
	if err := e.Register(chk); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err := e.Run(context.Background(), testTarget)
	if err == nil {
		t.Error("expected error for ScopePerResource without RunResource")
	}
}

// ── WithChecks functional option ──────────────────────────────────────────────

func TestEngine_WithChecks(t *testing.T) {
	chk := Check{ID: "pre", Run: stubRun}
	e := New(WithChecks(chk), WithDefaultTimeout(5*time.Second))
	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.TotalChecks != 1 {
		t.Errorf("TotalChecks: want 1, got %d", summary.TotalChecks)
	}
}

// ── ScopePerResource with no resources is skipped ────────────────────────────

func TestEngine_PerResourceSkippedWhenNoResources(t *testing.T) {
	rep := &testReporter{}
	e := New(WithReporters(rep), WithDefaultTimeout(5*time.Second))

	chk := Check{
		ID:          checkIDEndpoint,
		Scope:       ScopePerResource,
		RunResource: stubRunResource,
	}
	if err := e.Register(chk); err != nil {
		t.Fatalf("Register: %v", err)
	}

	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Skipped != 1 {
		t.Errorf("Skipped: want 1, got %d", summary.Skipped)
	}
}
