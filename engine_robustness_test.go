package harnessx

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	variantSlow = "slow-variant"
	idFirst     = "first"
	idSecond    = "second"
	idThird     = "third"
)

func TestRunVariants_SequentialStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var ran []string
	res := RunVariants(ctx, []string{"a", "b", "c"}, VariantsSequential, func(_ context.Context, v string) (Result, error) {
		ran = append(ran, v)
		if v == "a" {
			cancel()
		}
		return Result{}, nil
	})

	if !reflect.DeepEqual(ran, []string{"a"}) {
		t.Errorf("ran %v, want only [a] once ctx is cancelled", ran)
	}
	if len(res.Attempts) != 3 {
		t.Fatalf("Attempts = %d, want one per variant", len(res.Attempts))
	}
	for _, a := range res.Attempts[1:] {
		if !errors.Is(a.Err, context.Canceled) {
			t.Errorf("variant %q Err = %v, want context.Canceled", a.Variant, a.Err)
		}
	}
	if res.Attempts[0].Err != nil {
		t.Errorf("variant a Err = %v, want nil", res.Attempts[0].Err)
	}
}

func TestEngine_VariantConcurrencyLimitsParallelVariants(t *testing.T) {
	var inFlight, peak atomic.Int64
	chk := Check{
		ID: "limited", Scope: ScopeGlobal, VariantMode: VariantsParallel, VariantConcurrency: 2,
		Variants: []string{"a", "b", "c", "d", "e", "f"},
		RunVariant: func(context.Context, Target, string, ResultStore) (Result, error) {
			n := inFlight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			inFlight.Add(-1)
			return Result{}, nil
		},
	}
	if _, err := runEngine(t, chk); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := peak.Load(); got > 2 {
		t.Errorf("peak concurrent variants = %d, want <= 2", got)
	}
}

func TestEngine_VariantTimeoutBoundsEachVariant(t *testing.T) {
	chk := Check{
		ID: "variants-with-timeout", Scope: ScopeGlobal, Variants: []string{variantSlow, "fast"},
		VariantTimeout: 20 * time.Millisecond,
		RunVariant: func(ctx context.Context, _ Target, v string, _ ResultStore) (Result, error) {
			if v == variantSlow {
				<-ctx.Done()
				return Result{}, ctx.Err()
			}
			return Result{}, nil
		},
	}
	summary, err := runEngine(t, chk)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := resultFor(t, summary, "variants-with-timeout")
	if len(r.Attempts) != 2 || !errors.Is(r.Attempts[0].Err, context.DeadlineExceeded) || r.Attempts[1].Err != nil {
		t.Errorf("attempts = %+v, want slow to time out and fast to pass", r.Attempts)
	}
}

func TestEngine_CancellationRecordsEveryCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	e := New(WithMaxConcurrency(1))
	noop := func(context.Context, Target, ResultStore) (Result, error) { return Result{}, nil }
	if err := e.Register(
		globalCheck(idFirst, nil, func(context.Context, Target, ResultStore) (Result, error) {
			cancel()
			return Result{}, nil
		}),
		globalCheck(idSecond, []CheckID{idFirst}, noop),
		globalCheck(idThird, []CheckID{idSecond}, noop),
	); err != nil {
		t.Fatal(err)
	}

	summary, err := e.Run(ctx, Target{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if got := summary.Executed + summary.Skipped + summary.Failed; got != summary.TotalChecks {
		t.Errorf("Executed+Skipped+Failed = %d, want TotalChecks = %d", got, summary.TotalChecks)
	}
	for _, id := range []CheckID{idSecond, idThird} {
		if r := resultFor(t, summary, id); !r.Skipped || r.SkipReason != reasonCancelled {
			t.Errorf("%s = %+v, want skipped with %q", id, r, reasonCancelled)
		}
	}
}

func TestEngine_SummaryOrderIsDeterministic(t *testing.T) {
	obs := func(id CheckID) CheckFunc {
		return func(context.Context, Target, ResultStore) (Result, error) {
			time.Sleep(time.Duration(len(id)) * time.Millisecond) // finish out of ID order
			return Result{Observations: []Observation{{CheckID: id, Title: string(id)}}}, nil
		}
	}
	var want []string
	for range 5 {
		e := New(WithMaxConcurrency(8))
		if err := e.Register(
			globalCheck("ccc", nil, obs("ccc")),
			globalCheck("a", nil, obs("a")),
			globalCheck("bb", nil, obs("bb")),
			globalCheck("z-after", []CheckID{"a"}, obs("z-after")),
		); err != nil {
			t.Fatal(err)
		}
		summary, err := e.Run(context.Background(), Target{})
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(summary.Observations))
		for _, o := range summary.Observations {
			got = append(got, o.Title)
		}
		if want == nil {
			want = got
			if !reflect.DeepEqual(want, []string{"a", "bb", "ccc", "z-after"}) {
				t.Fatalf("observations = %v, want by level then ID: [a bb ccc z-after]", want)
			}
		} else if !reflect.DeepEqual(got, want) {
			t.Fatalf("observations = %v, want %v on every run", got, want)
		}
	}
}

func TestEngine_PerResourceSkipReasonWithDependsOn(t *testing.T) {
	summary, err := runEngine(t,
		discover(idDiscover),
		resourceCheck("empty", []CheckID{idDiscover}, func(context.Context, Target, Resource, ResultStore) (Result, error) {
			return Result{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, summary, "empty").SkipReason; got != "no resources discovered" {
		t.Errorf("SkipReason = %q, want %q", got, "no resources discovered")
	}
}

func TestLevels_ErrorsNameTheChecks(t *testing.T) {
	_, err := levels([]Check{{ID: "a", DependsOn: []CheckID{"ghost"}}})
	if !errors.Is(err, ErrUnknownDependency) || !strings.Contains(err.Error(), `"a"`) || !strings.Contains(err.Error(), `"ghost"`) {
		t.Errorf("unknown dependency error = %v, want it to name a and ghost", err)
	}

	_, err = levels([]Check{
		{ID: "x", DependsOn: []CheckID{"y"}},
		{ID: "y", DependsOn: []CheckID{"x"}},
		{ID: "free"},
	})
	if !errors.Is(err, ErrCycleDetected) || !strings.Contains(err.Error(), "x, y") || strings.Contains(err.Error(), "free") {
		t.Errorf("cycle error = %v, want it to list x, y only", err)
	}
}

func TestEngine_WithOnlyUnknownIDIsAnError(t *testing.T) {
	e := New()
	if err := e.Register(globalCheck("real", nil, stubRun)); err != nil {
		t.Fatal(err)
	}

	_, err := e.Run(context.Background(), Target{}, WithOnly("real", "typo", "oops"))
	if !errors.Is(err, ErrUnknownCheckID) || !strings.Contains(err.Error(), "oops, typo") {
		t.Errorf("Run error = %v, want ErrUnknownCheckID naming oops and typo", err)
	}

	_, err = e.RunScenario(context.Background(), Target{}, Scenario{Checks: []Check{globalCheck("real", nil, stubRun)}}, WithOnly("typo"))
	if !errors.Is(err, ErrUnknownCheckID) {
		t.Errorf("RunScenario error = %v, want ErrUnknownCheckID", err)
	}

	if _, err := e.Run(context.Background(), Target{}, WithOnly("real")); err != nil {
		t.Errorf("Run with a valid WithOnly: %v", err)
	}
}

func TestStaticResultStore_PerResourceSeeding(t *testing.T) {
	store := NewStaticResultStore(
		Result{CheckID: "scan", ResourceID: "r1", Observations: []Observation{{Title: "one"}}},
		Result{CheckID: "scan", ResourceID: "r2"},
		Result{CheckID: "global", Data: 7},
	).WithResources(Resource{ID: "r1"}, Resource{ID: "r2"})

	if r, ok := store.GetForResource("scan", "r1"); !ok || len(r.Observations) != 1 {
		t.Errorf("GetForResource(scan, r1) = %+v, %v; want its own result", r, ok)
	}
	if r, ok := store.GetForResource("scan", "r2"); !ok || len(r.Observations) != 0 {
		t.Errorf("GetForResource(scan, r2) = %+v, %v; want r2's result, not r1's", r, ok)
	}
	if r, ok := store.Get("scan"); !ok || len(r.Observations) != 1 {
		t.Errorf("Get(scan) = %+v, %v; want the roll-up of r1 and r2", r, ok)
	}
	if !IfCheckObserved("scan")(store) {
		t.Error("IfCheckObserved(scan) = false, want true via the roll-up")
	}
	if v, ok := GetData[int](store, "global"); !ok || v != 7 {
		t.Errorf("GetData(global) = %v, %v", v, ok)
	}
	if got := len(store.Resources()); got != 2 {
		t.Errorf("Resources() = %d, want 2", got)
	}
	if got := len(store.Observations()); got != 1 {
		t.Errorf("Observations() = %d, want 1", got)
	}
	if _, ok := store.Get("missing"); ok {
		t.Error("Get(missing) = ok")
	}
}

func TestBaselineChecks_MisconfiguredReturnErrorInsteadOfPanicking(t *testing.T) {
	ctx := context.Background()
	for name, run := range map[string]func() (Result, error){
		"baseline": func() (Result, error) {
			return NewBaselineCheck(BaselineCheckConfig{ID: "b"}).RunResource(ctx, Target{}, Resource{}, NewStaticResultStore())
		},
		"global baseline": func() (Result, error) {
			return NewGlobalBaselineCheck(GlobalBaselineCheckConfig{ID: "b"}).Run(ctx, Target{}, NewStaticResultStore())
		},
		"capture": func() (Result, error) {
			return NewCaptureBaselineCheck(CaptureBaselineCheckConfig{ID: "b"}).RunResource(ctx, Target{}, Resource{}, NewStaticResultStore())
		},
		"global capture": func() (Result, error) {
			return NewCaptureGlobalBaselineCheck(CaptureGlobalBaselineCheckConfig{ID: "b"}).Run(ctx, Target{}, NewStaticResultStore())
		},
	} {
		_, err := run()
		if err == nil || !strings.Contains(err.Error(), "misconfigured") {
			t.Errorf("%s: error = %v, want a misconfiguration error", name, err)
		}
	}
}

func TestStaticResultStore_FallbackAndOrdering(t *testing.T) {
	store := NewStaticResultStore(
		Result{CheckID: "b", ResourceID: "r2", Observations: []Observation{{Title: "b-r2"}}},
		Result{CheckID: "b", ResourceID: "r1", Observations: []Observation{{Title: "b-r1"}}},
		Result{CheckID: "a", ResourceID: "r1", Observations: []Observation{{Title: "a-r1"}}},
		Result{CheckID: "g", Observations: []Observation{{Title: "g"}}},
	)

	observations := store.Observations()
	got := make([]string, 0, len(observations))
	for _, o := range observations {
		got = append(got, o.Title)
	}
	if want := []string{"g", "a-r1", "b-r1", "b-r2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Observations = %v, want global results first, then per-resource by check and resource: %v", got, want)
	}

	if r, ok := store.GetForResource("g", "anything"); !ok || r.Observations[0].Title != "g" {
		t.Errorf("GetForResource(g, anything) = %+v, %v; want the check-wide result as a fallback", r, ok)
	}
	if _, ok := store.GetForResource("a", "r9"); ok {
		t.Error("GetForResource(a, r9) = ok, want nothing: a has only per-resource results and none for r9")
	}
}

func TestScanError_MessageAndUnwrap(t *testing.T) {
	cause := errors.New("boom")
	err := &ScanError{CheckID: "chk", Cause: cause}
	if got := err.Error(); got != `harnessx: check "chk" failed: boom` {
		t.Errorf("Error() = %q", got)
	}
	if !errors.Is(err, cause) {
		t.Error("ScanError does not unwrap to its cause")
	}
}

func TestNewCaptureBaselineCheck_CaptureErrorFailsTheResult(t *testing.T) {
	boom := errors.New("capture failed")
	_, err := NewCaptureBaselineCheck(CaptureBaselineCheckConfig{
		ID: "c",
		Capture: func(context.Context, Target, Resource, ResultStore) (Snapshot, error) {
			return Snapshot{}, boom
		},
	}).RunResource(context.Background(), Target{}, Resource{}, NewStaticResultStore())
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want the capture error", err)
	}
}

func TestRunVariants_ParallelLimitStopsWaitingOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	var ran atomic.Int64
	done := make(chan Result, 1)
	go func() {
		done <- runVariants(ctx, []string{"a", "b"}, VariantsParallel, variantLimits{concurrency: 1}, func(vctx context.Context, _ string) (Result, error) {
			if ran.Add(1) == 1 {
				close(started)
			}
			<-vctx.Done() // whichever variant got the only slot holds it until cancellation
			return Result{}, nil
		})
	}()

	<-started
	cancel()
	res := <-done

	if got := ran.Load(); got != 1 {
		t.Errorf("%d variants ran, want 1: the other was waiting for a slot", got)
	}
	cancelled := 0
	for _, a := range res.Attempts {
		if errors.Is(a.Err, context.Canceled) {
			cancelled++
		}
	}
	if cancelled != 1 {
		t.Errorf("attempts = %+v, want exactly one recorded as cancelled", res.Attempts)
	}
}

func TestEngine_LevelSlotWaitersAreRecordedOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	e := New(WithMaxConcurrency(1))
	if err := e.Register(
		globalCheck("holder", nil, func(context.Context, Target, ResultStore) (Result, error) {
			cancel()
			<-release
			return Result{}, nil
		}),
		globalCheck("waiter", nil, stubRun),
	); err != nil {
		t.Fatal(err)
	}
	go func() {
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // let the waiter observe the cancellation
		close(release)
	}()

	summary, err := e.Run(ctx, Target{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if got := summary.Executed + summary.Skipped + summary.Failed; got != 2 {
		t.Errorf("Executed+Skipped+Failed = %d, want 2: the check waiting for a slot must be recorded", got)
	}
}

func TestEngine_RunScenarioRejectsDuplicateIDs(t *testing.T) {
	_, err := New().RunScenario(context.Background(), Target{}, Scenario{Checks: []Check{
		globalCheck("dup", nil, stubRun), globalCheck("dup", nil, stubRun),
	}})
	if !errors.Is(err, ErrDuplicateCheckID) {
		t.Errorf("error = %v, want ErrDuplicateCheckID", err)
	}
}
