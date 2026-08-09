package harnessx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cerberauth/harnessx/probe"
)

const (
	testCheckIDProbe     = "probe"
	testCheckIDCheck     = "check"
	testCheckIDDiscovery = "discovery"

	testTitleAuthBypass = "Authorization bypass"
)

// fakeStore is a minimal ResultStore for unit-testing BaselineSource
// implementations without spinning up a full engine run.
type fakeStore struct {
	global map[CheckID]Result
	perRes map[checkResourceKey]Result
}

func (f fakeStore) Get(id CheckID) (Result, bool) {
	r, ok := f.global[id]
	return r, ok
}

func (f fakeStore) GetForResource(id CheckID, resourceID string) (Result, bool) {
	r, ok := f.perRes[checkResourceKey{id, resourceID}]
	return r, ok
}

func (f fakeStore) Observations() []Observation { return nil }
func (f fakeStore) Resources() []Resource       { return nil }

// --- CompareStatusCode ---

func TestCompareStatusCode_Match(t *testing.T) {
	obs := CompareStatusCode(Snapshot{StatusCode: 401}, Snapshot{StatusCode: 401})
	if obs != nil {
		t.Errorf("obs = %v, want nil", obs)
	}
}

func TestCompareStatusCode_Mismatch(t *testing.T) {
	obs := CompareStatusCode(Snapshot{StatusCode: 401}, Snapshot{StatusCode: 200})
	if len(obs) != 1 {
		t.Fatalf("obs = %v, want 1 observation", obs)
	}
	if obs[0].Title == "" {
		t.Error("expected non-empty Title")
	}
}

// --- BaselineSource variants ---

func TestStaticBaseline(t *testing.T) {
	src := StaticBaseline(Baseline{StatusCode: 403})
	b, ok := src(context.Background(), testTarget, Resource{ID: "r1"}, fakeStore{})
	if !ok || b.StatusCode != 403 {
		t.Errorf("b = %v, ok = %v, want {403} true", b, ok)
	}
}

func TestBaselineFromResource_Found(t *testing.T) {
	res := Resource{ID: "r1", Data: Baseline{StatusCode: 401}}
	src := BaselineFromResource()
	b, ok := src(context.Background(), testTarget, res, fakeStore{})
	if !ok || b.StatusCode != 401 {
		t.Errorf("b = %v, ok = %v, want {401} true", b, ok)
	}
}

func TestBaselineFromResource_NotFound(t *testing.T) {
	res := Resource{ID: "r1"}
	src := BaselineFromResource()
	_, ok := src(context.Background(), testTarget, res, fakeStore{})
	if ok {
		t.Error("ok = true, want false for resource with no baseline attached")
	}
}

func TestBaselineFromCheck_Found(t *testing.T) {
	store := fakeStore{perRes: map[checkResourceKey]Result{
		{testCheckIDProbe, "r1"}: {Data: Baseline{StatusCode: 401}},
	}}
	src := BaselineFromCheck(testCheckIDProbe)
	b, ok := src(context.Background(), testTarget, Resource{ID: "r1"}, store)
	if !ok || b.StatusCode != 401 {
		t.Errorf("b = %v, ok = %v, want {401} true", b, ok)
	}
}

func TestBaselineFromCheck_NotFound(t *testing.T) {
	src := BaselineFromCheck(testCheckIDProbe)
	_, ok := src(context.Background(), testTarget, Resource{ID: "r1"}, fakeStore{})
	if ok {
		t.Error("ok = true, want false when no result recorded for check+resource")
	}
}

func TestBaselineFromCheck_WrongDataType(t *testing.T) {
	store := fakeStore{perRes: map[checkResourceKey]Result{
		{testCheckIDProbe, "r1"}: {Data: "not a baseline"},
	}}
	src := BaselineFromCheck(testCheckIDProbe)
	_, ok := src(context.Background(), testTarget, Resource{ID: "r1"}, store)
	if ok {
		t.Error("ok = true, want false when Result.Data isn't a Baseline")
	}
}

// --- NewBaselineCheck / CaptureBaselineCheck, end to end ---

func TestNewBaselineCheck_DefaultComparator_NoDeviation(t *testing.T) {
	check := NewBaselineCheck(BaselineCheckConfig{
		ID:       testCheckIDCheck,
		Baseline: StaticBaseline(Baseline{StatusCode: 401}),
		Capture: func(context.Context, Target, Resource, ResultStore) (Snapshot, error) {
			return Snapshot{StatusCode: 401}, nil
		},
	})

	discovery := Check{
		ID:    testCheckIDDiscovery,
		Scope: ScopeGlobal,
		Run: func(context.Context, Target, ResultStore) (Result, error) {
			return Result{Resources: []Resource{{ID: "r1"}}}, nil
		},
	}
	check.DependsOn = []CheckID{testCheckIDDiscovery}

	e := New(WithChecks(discovery, check))
	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Observations) != 0 {
		t.Errorf("Observations = %v, want none", summary.Observations)
	}
}

func TestNewBaselineCheck_DefaultComparator_Deviation(t *testing.T) {
	check := NewBaselineCheck(BaselineCheckConfig{
		ID:       testCheckIDCheck,
		Baseline: StaticBaseline(Baseline{StatusCode: 401}),
		Capture: func(context.Context, Target, Resource, ResultStore) (Snapshot, error) {
			return Snapshot{StatusCode: 200}, nil
		},
	})

	discovery := Check{
		ID:    testCheckIDDiscovery,
		Scope: ScopeGlobal,
		Run: func(context.Context, Target, ResultStore) (Result, error) {
			return Result{Resources: []Resource{{ID: "r1"}}}, nil
		},
	}
	check.DependsOn = []CheckID{testCheckIDDiscovery}

	e := New(WithChecks(discovery, check))
	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Observations) != 1 {
		t.Fatalf("Observations = %v, want 1", summary.Observations)
	}
}

func TestNewBaselineCheck_NoBaseline_Skips(t *testing.T) {
	check := NewBaselineCheck(BaselineCheckConfig{
		ID: testCheckIDCheck,
		Baseline: func(context.Context, Target, Resource, ResultStore) (Baseline, bool) {
			return Baseline{}, false
		},
		Capture: func(context.Context, Target, Resource, ResultStore) (Snapshot, error) {
			t.Fatal("Capture should not be called when no baseline is available")
			return Snapshot{}, nil
		},
	})

	discovery := Check{
		ID:    testCheckIDDiscovery,
		Scope: ScopeGlobal,
		Run: func(context.Context, Target, ResultStore) (Result, error) {
			return Result{Resources: []Resource{{ID: "r1"}}}, nil
		},
	}
	check.DependsOn = []CheckID{testCheckIDDiscovery}

	e := New(WithChecks(discovery, check))
	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var found bool
	for _, r := range summary.Results {
		if r.CheckID == testCheckIDCheck {
			found = true
			if !r.Skipped || r.SkipReason == "" {
				t.Errorf("Result = %+v, want Skipped=true with a reason", r)
			}
		}
	}
	if !found {
		t.Fatal("no result recorded for check \"check\"")
	}
}

func TestNewBaselineCheck_CustomComparator(t *testing.T) {
	bypass := func(baseline, current Snapshot) []Observation {
		if baseline.StatusCode >= 300 && current.StatusCode < 300 {
			return []Observation{{Title: testTitleAuthBypass}}
		}
		return nil
	}

	check := NewBaselineCheck(BaselineCheckConfig{
		ID:       testCheckIDCheck,
		Baseline: StaticBaseline(Baseline{StatusCode: 401}),
		Capture: func(context.Context, Target, Resource, ResultStore) (Snapshot, error) {
			return Snapshot{StatusCode: 200}, nil
		},
		Compare: bypass,
	})

	discovery := Check{
		ID:    testCheckIDDiscovery,
		Scope: ScopeGlobal,
		Run: func(context.Context, Target, ResultStore) (Result, error) {
			return Result{Resources: []Resource{{ID: "r1"}}}, nil
		},
	}
	check.DependsOn = []CheckID{testCheckIDDiscovery}

	e := New(WithChecks(discovery, check))
	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Observations) != 1 || summary.Observations[0].Title != testTitleAuthBypass {
		t.Fatalf("Observations = %v, want [Authorization bypass]", summary.Observations)
	}
}

func TestCaptureBaselineCheck_FeedsBaselineFromCheck(t *testing.T) {
	probeCheck := CaptureBaselineCheck(testCheckIDProbe, "Baseline Probe", func(context.Context, Target, Resource, ResultStore) (Snapshot, error) {
		return Snapshot{StatusCode: 401}, nil
	})

	compareCheck := NewBaselineCheck(BaselineCheckConfig{
		ID:        "compare",
		DependsOn: []CheckID{testCheckIDProbe},
		Baseline:  BaselineFromCheck(testCheckIDProbe),
		Capture: func(context.Context, Target, Resource, ResultStore) (Snapshot, error) {
			return Snapshot{StatusCode: 200}, nil
		},
	})

	discovery := Check{
		ID:    testCheckIDDiscovery,
		Scope: ScopeGlobal,
		Run: func(context.Context, Target, ResultStore) (Result, error) {
			return Result{Resources: []Resource{{ID: "r1"}}}, nil
		},
	}
	probeCheck.DependsOn = []CheckID{testCheckIDDiscovery}

	e := New(WithChecks(discovery, probeCheck, compareCheck))
	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Observations) != 1 {
		t.Fatalf("Observations = %v, want 1 (401 baseline vs 200 current)", summary.Observations)
	}
}

func TestStaticGlobalBaseline(t *testing.T) {
	src := StaticGlobalBaseline(Baseline{StatusCode: 403})
	b, ok := src(context.Background(), testTarget, fakeStore{})
	if !ok || b.StatusCode != 403 {
		t.Errorf("b = %v, ok = %v, want {403} true", b, ok)
	}
}

func TestBaselineFromGlobalCheck_Found(t *testing.T) {
	store := fakeStore{global: map[CheckID]Result{
		testCheckIDProbe: {Data: Baseline{StatusCode: 401}},
	}}
	src := BaselineFromGlobalCheck(testCheckIDProbe)
	b, ok := src(context.Background(), testTarget, store)
	if !ok || b.StatusCode != 401 {
		t.Errorf("b = %v, ok = %v, want {401} true", b, ok)
	}
}

func TestBaselineFromGlobalCheck_NotFound(t *testing.T) {
	src := BaselineFromGlobalCheck(testCheckIDProbe)
	_, ok := src(context.Background(), testTarget, fakeStore{})
	if ok {
		t.Error("ok = true, want false when no global result recorded for check")
	}
}

func TestBaselineFromGlobalCheck_WrongDataType(t *testing.T) {
	store := fakeStore{global: map[CheckID]Result{
		testCheckIDProbe: {Data: "not a baseline"},
	}}
	src := BaselineFromGlobalCheck(testCheckIDProbe)
	_, ok := src(context.Background(), testTarget, store)
	if ok {
		t.Error("ok = true, want false when Result.Data isn't a Baseline")
	}
}

func TestNewGlobalBaselineCheck_DefaultComparator_NoDeviation(t *testing.T) {
	check := NewGlobalBaselineCheck(GlobalBaselineCheckConfig{
		ID:       testCheckIDCheck,
		Baseline: StaticGlobalBaseline(Baseline{StatusCode: 401}),
		Capture: func(context.Context, Target, ResultStore) (Snapshot, error) {
			return Snapshot{StatusCode: 401}, nil
		},
	})

	e := New(WithChecks(check))
	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Observations) != 0 {
		t.Errorf("Observations = %v, want none", summary.Observations)
	}
}

func TestNewGlobalBaselineCheck_DefaultComparator_Deviation(t *testing.T) {
	check := NewGlobalBaselineCheck(GlobalBaselineCheckConfig{
		ID:       testCheckIDCheck,
		Baseline: StaticGlobalBaseline(Baseline{StatusCode: 401}),
		Capture: func(context.Context, Target, ResultStore) (Snapshot, error) {
			return Snapshot{StatusCode: 200}, nil
		},
	})

	e := New(WithChecks(check))
	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Observations) != 1 {
		t.Fatalf("Observations = %v, want 1", summary.Observations)
	}
}

func TestNewGlobalBaselineCheck_NoBaseline_Skips(t *testing.T) {
	check := NewGlobalBaselineCheck(GlobalBaselineCheckConfig{
		ID: testCheckIDCheck,
		Baseline: func(context.Context, Target, ResultStore) (Baseline, bool) {
			return Baseline{}, false
		},
		Capture: func(context.Context, Target, ResultStore) (Snapshot, error) {
			t.Fatal("Capture should not be called when no baseline is available")
			return Snapshot{}, nil
		},
	})

	e := New(WithChecks(check))
	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var found bool
	for _, r := range summary.Results {
		if r.CheckID == testCheckIDCheck {
			found = true
			if !r.Skipped || r.SkipReason == "" {
				t.Errorf("Result = %+v, want Skipped=true with a reason", r)
			}
		}
	}
	if !found {
		t.Fatal("no result recorded for check \"check\"")
	}
}

func TestNewGlobalBaselineCheck_CustomComparator(t *testing.T) {
	bypass := func(baseline, current Snapshot) []Observation {
		if baseline.StatusCode >= 300 && current.StatusCode < 300 {
			return []Observation{{Title: testTitleAuthBypass}}
		}
		return nil
	}

	check := NewGlobalBaselineCheck(GlobalBaselineCheckConfig{
		ID:       testCheckIDCheck,
		Baseline: StaticGlobalBaseline(Baseline{StatusCode: 401}),
		Capture: func(context.Context, Target, ResultStore) (Snapshot, error) {
			return Snapshot{StatusCode: 200}, nil
		},
		Compare: bypass,
	})

	e := New(WithChecks(check))
	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Observations) != 1 || summary.Observations[0].Title != testTitleAuthBypass {
		t.Fatalf("Observations = %v, want [Authorization bypass]", summary.Observations)
	}
}

func TestCaptureGlobalBaselineCheck_FeedsBaselineFromGlobalCheck(t *testing.T) {
	probeCheck := CaptureGlobalBaselineCheck(testCheckIDProbe, "Baseline Probe", func(context.Context, Target, ResultStore) (Snapshot, error) {
		return Snapshot{StatusCode: 401}, nil
	})

	compareCheck := NewGlobalBaselineCheck(GlobalBaselineCheckConfig{
		ID:        "compare",
		DependsOn: []CheckID{testCheckIDProbe},
		Baseline:  BaselineFromGlobalCheck(testCheckIDProbe),
		Capture: func(context.Context, Target, ResultStore) (Snapshot, error) {
			return Snapshot{StatusCode: 200}, nil
		},
	})

	e := New(WithChecks(probeCheck, compareCheck))
	summary, err := e.Run(context.Background(), testTarget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Observations) != 1 {
		t.Fatalf("Observations = %v, want 1 (401 baseline vs 200 current)", summary.Observations)
	}
}

// --- ProbeAndCompareBaseline ---

func TestProbeAndCompareBaseline_VulnerableWhenStatusDiffersFromBaseline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := NewStaticResultStore(ResultData(testCheckIDProbe, Snapshot{StatusCode: 401}))
	build := func(ctx context.Context) (*http.Request, error) {
		return probe.NewRequest(ctx, http.MethodGet, srv.URL, nil, probe.WithBearerToken("tok"))
	}
	snap, vulnerable, err := ProbeAndCompareBaseline(context.Background(), probe.New(), build, store, testCheckIDProbe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !vulnerable {
		t.Error("vulnerable = false, want true")
	}
	if snap.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", snap.StatusCode, http.StatusOK)
	}
}

func TestProbeAndCompareBaseline_NotVulnerableWhenStatusMatchesBaseline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	store := NewStaticResultStore(ResultData(testCheckIDProbe, Snapshot{StatusCode: http.StatusUnauthorized}))
	build := func(ctx context.Context) (*http.Request, error) {
		return probe.NewRequest(ctx, http.MethodGet, srv.URL, nil, probe.WithBearerToken("tok"))
	}
	_, vulnerable, err := ProbeAndCompareBaseline(context.Background(), probe.New(), build, store, testCheckIDProbe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vulnerable {
		t.Error("vulnerable = true, want false")
	}
}

func TestProbeAndCompareBaseline_InvalidTarget_ReturnsError(t *testing.T) {
	store := NewStaticResultStore()
	build := func(ctx context.Context) (*http.Request, error) {
		return probe.NewRequest(ctx, http.MethodGet, "://bad-url", nil, probe.WithBearerToken("tok"))
	}
	_, _, err := ProbeAndCompareBaseline(context.Background(), probe.New(), build, store, testCheckIDProbe)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestProbeAndCompareBaseline_ClientDoError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Close() // closed server: connection refused

	store := NewStaticResultStore(ResultData(testCheckIDProbe, Snapshot{StatusCode: http.StatusOK}))
	build := func(ctx context.Context) (*http.Request, error) {
		return probe.NewRequest(ctx, http.MethodGet, srv.URL, nil, probe.WithBearerToken("tok"))
	}
	_, _, err := ProbeAndCompareBaseline(context.Background(), probe.New(), build, store, testCheckIDProbe)
	if err == nil {
		t.Error("expected error, got nil")
	}
}
