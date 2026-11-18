package embed_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/cerberauth/harnessx"
	"github.com/cerberauth/harnessx/embed"
)

const (
	idBaseline   = "baseline"
	jwtPrefix    = "jwt."
	cacheCheck   = "cache.check"
	hostSaysNo   = "host says no"
	hostDiscover = "host.discover"
)

type packCtx struct{ resource string }

type collector struct {
	mu      sync.Mutex
	results []harnessx.Result
}

func (c *collector) OnScanStart(harnessx.Target, int)                                 {}
func (c *collector) OnCheckStart(harnessx.Check, harnessx.Target, *harnessx.Resource) {}
func (c *collector) OnScanComplete(harnessx.ScanSummary)                              {}
func (c *collector) OnCheckComplete(r harnessx.Result) {
	c.mu.Lock()
	c.results = append(c.results, r)
	c.mu.Unlock()
}

func discover(ids ...string) harnessx.Check {
	return harnessx.Check{
		ID:    hostDiscover,
		Scope: harnessx.ScopeGlobal,
		Run: func(context.Context, harnessx.Target, harnessx.ResultStore) (harnessx.Result, error) {
			res := make([]harnessx.Resource, 0, len(ids))
			for _, id := range ids {
				res = append(res, harnessx.Resource{ID: id, URL: "http://example.test/" + id})
			}
			return harnessx.Result{Resources: res}, nil
		},
	}
}

func globalPack() []harnessx.Check {
	return []harnessx.Check{
		{
			ID:       idBaseline,
			Internal: true,
			Scope:    harnessx.ScopeGlobal,
			Run: func(_ context.Context, target harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
				return harnessx.Result{Data: target.Data.(*packCtx).resource + "@" + target.URL}, nil
			},
		},
		{
			ID:        "probe",
			Scope:     harnessx.ScopeGlobal,
			DependsOn: []harnessx.CheckID{idBaseline},
			Run: func(_ context.Context, _ harnessx.Target, store harnessx.ResultStore) (harnessx.Result, error) {
				base, ok := harnessx.GetData[string](store, idBaseline)
				if !ok {
					return harnessx.Result{}, errors.New("no baseline")
				}
				return harnessx.Result{Observations: []harnessx.Observation{{Title: base}}}, nil
			},
		},
	}
}

func TestGlobalPackPerResourceReadsOwnDependency(t *testing.T) {
	checks, err := embed.Checks(globalPack(), embed.Options{
		Scope:     harnessx.ScopePerResource,
		Prefix:    jwtPrefix,
		DependsOn: []harnessx.CheckID{hostDiscover},
		Context: func(_ context.Context, _ harnessx.Target, res *harnessx.Resource) (any, error) {
			return &packCtx{resource: res.ID}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rep := &collector{}
	e := harnessx.New(harnessx.WithChecks(append([]harnessx.Check{discover("a", "b")}, checks...)...), harnessx.WithReporters(rep))
	summary, err := e.Run(context.Background(), harnessx.Target{URL: "http://example.test"})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, o := range summary.Observations {
		if o.CheckID != "jwt.probe" {
			t.Errorf("observation CheckID = %q, want jwt.probe", o.CheckID)
		}
		got[o.ResourceID] = o.Title
	}
	want := map[string]string{"a": "a@http://example.test/a", "b": "b@http://example.test/b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("observations = %v, want %v", got, want)
	}
	for _, r := range append(summary.Results, rep.results...) {
		if r.CheckID == "jwt.baseline" || r.CheckID == "jwt.context" {
			t.Errorf("internal check %q reported", r.CheckID)
		}
	}
}

func TestPerResourcePackOnGlobalTargetRunsOnce(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	pack := []harnessx.Check{
		{
			ID:    "cache.seed",
			Scope: harnessx.ScopePerResource,
			RunResource: func(_ context.Context, _ harnessx.Target, res harnessx.Resource, _ harnessx.ResultStore) (harnessx.Result, error) {
				return harnessx.Result{Data: res.URL}, nil
			},
		},
		{
			ID:        cacheCheck,
			Scope:     harnessx.ScopePerResource,
			DependsOn: []harnessx.CheckID{"cache.seed"},
			RunResource: func(_ context.Context, target harnessx.Target, res harnessx.Resource, store harnessx.ResultStore) (harnessx.Result, error) {
				mu.Lock()
				calls++
				mu.Unlock()
				seed, ok := store.GetForResource("cache.seed", res.ID)
				if !ok || seed.Data != target.URL {
					return harnessx.Result{}, errors.New("seed not visible")
				}
				if n := len(store.Resources()); n != 1 {
					return harnessx.Result{}, errors.New("expected a single resource")
				}
				return harnessx.Result{Observations: []harnessx.Observation{{CheckID: cacheCheck, Title: target.Data.(string)}}}, nil
			},
		},
	}
	checks, err := embed.Checks(pack, embed.Options{
		Scope:  harnessx.ScopeGlobal,
		Prefix: "proxy.",
		Context: func(_ context.Context, target harnessx.Target, res *harnessx.Resource) (any, error) {
			if res != nil {
				return nil, errors.New("unexpected resource")
			}
			return "ctx:" + target.URL, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if checks[0].ID != "proxy.context" || !checks[0].Internal {
		t.Fatalf("context check = %q internal=%v", checks[0].ID, checks[0].Internal)
	}

	summary, err := harnessx.New(harnessx.WithChecks(checks...)).Run(context.Background(), harnessx.Target{URL: "http://example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
	if len(summary.Observations) != 1 || summary.Observations[0].CheckID != "proxy.cache.check" || summary.Observations[0].Title != "ctx:http://example.test" {
		t.Errorf("observations = %+v", summary.Observations)
	}
}

func variantPack() []harnessx.Check {
	return []harnessx.Check{{
		ID:          "jwt.alg_none",
		Scope:       harnessx.ScopeGlobal,
		Variants:    []string{"none", "NONE", "None"},
		VariantMode: harnessx.VariantsParallel,
		RunVariant: func(_ context.Context, _ harnessx.Target, variant string, _ harnessx.ResultStore) (harnessx.Result, error) {
			if variant == "NONE" {
				return harnessx.Result{Observations: []harnessx.Observation{{Title: "accepted"}}, Data: variant}, nil
			}
			return harnessx.Result{Skipped: variant == "None", Data: variant}, nil
		},
	}}
}

func attemptsOf(t *testing.T, summary harnessx.ScanSummary, id harnessx.CheckID) []harnessx.Attempt {
	t.Helper()
	for _, r := range summary.Results {
		if r.CheckID == id {
			out := make([]harnessx.Attempt, len(r.Attempts))
			for i, a := range r.Attempts {
				a.Duration = 0
				for j := range a.Observations {
					a.Observations[j].CheckID, a.Observations[j].ResourceID = "", ""
				}
				out[i] = a
			}
			return out
		}
	}
	t.Fatalf("no result for %q", id)
	return nil
}

func TestVariantAttemptsMatchEngine(t *testing.T) {
	ctx := context.Background()
	direct, err := harnessx.New(harnessx.WithChecks(variantPack()...)).Run(ctx, harnessx.Target{})
	if err != nil {
		t.Fatal(err)
	}

	checks := embed.MustChecks(variantPack(), embed.Options{Scope: harnessx.ScopePerResource, DependsOn: []harnessx.CheckID{hostDiscover}})
	embedded, err := harnessx.New(harnessx.WithChecks(append([]harnessx.Check{discover("a")}, checks...)...)).Run(ctx, harnessx.Target{})
	if err != nil {
		t.Fatal(err)
	}

	want := attemptsOf(t, direct, "jwt.alg_none")
	got := attemptsOf(t, embedded, "jwt.alg_none")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attempts through embed = %+v, want %+v", got, want)
	}
}

func TestTwoPacksWithSameIDsSideBySide(t *testing.T) {
	pack := func(name string) []harnessx.Check {
		return []harnessx.Check{
			{ID: idBaseline, Scope: harnessx.ScopeGlobal, Run: func(context.Context, harnessx.Target, harnessx.ResultStore) (harnessx.Result, error) {
				return harnessx.Result{Data: name}, nil
			}},
			{ID: "check", Scope: harnessx.ScopeGlobal, DependsOn: []harnessx.CheckID{idBaseline}, Run: func(_ context.Context, _ harnessx.Target, store harnessx.ResultStore) (harnessx.Result, error) {
				v, _ := harnessx.GetData[string](store, idBaseline)
				return harnessx.Result{Observations: []harnessx.Observation{{Title: v}}}, nil
			}},
		}
	}

	if err := harnessx.New().Register(append(pack("a"), pack("b")...)...); !errors.Is(err, harnessx.ErrDuplicateCheckID) {
		t.Fatalf("unprefixed packs: err = %v, want ErrDuplicateCheckID", err)
	}

	a := embed.MustChecks(pack("a"), embed.Options{Prefix: jwtPrefix})
	b := embed.MustChecks(pack("b"), embed.Options{Prefix: "cache."})
	e := harnessx.New()
	if err := e.Register(append(a, b...)...); err != nil {
		t.Fatal(err)
	}
	summary, err := e.Run(context.Background(), harnessx.Target{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[harnessx.CheckID]string{}
	for _, o := range summary.Observations {
		got[o.CheckID] = o.Title
	}
	want := map[harnessx.CheckID]string{"jwt.check": "a", cacheCheck: "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("observations = %v, want %v", got, want)
	}
}

func TestSkipAndConditionsKept(t *testing.T) {
	pack := []harnessx.Check{
		{ID: "dep", Scope: harnessx.ScopeGlobal, Run: func(context.Context, harnessx.Target, harnessx.ResultStore) (harnessx.Result, error) {
			return harnessx.Result{Observations: []harnessx.Observation{{Title: "x"}}}, nil
		}},
		{ID: "cond", Scope: harnessx.ScopeGlobal, DependsOn: []harnessx.CheckID{"dep"}, Conditions: []harnessx.Condition{harnessx.IfCheckPassed("dep")},
			Run: func(context.Context, harnessx.Target, harnessx.ResultStore) (harnessx.Result, error) {
				return harnessx.Result{}, nil
			}},
		{ID: "skip", Scope: harnessx.ScopeGlobal, Skip: harnessx.SkipAlways("pack says no"),
			Run: func(context.Context, harnessx.Target, harnessx.ResultStore) (harnessx.Result, error) {
				return harnessx.Result{}, nil
			}},
	}
	checks := embed.MustChecks(pack, embed.Options{
		Scope:     harnessx.ScopePerResource,
		Prefix:    "p.",
		DependsOn: []harnessx.CheckID{hostDiscover},
		Skip: harnessx.SkipResourceWhen(func(_ context.Context, _ harnessx.Target, res harnessx.Resource, _ harnessx.ResultStore) string {
			if res.ID == "b" {
				return hostSaysNo
			}
			return ""
		}),
	})
	summary, err := harnessx.New(harnessx.WithChecks(append([]harnessx.Check{discover("a", "b")}, checks...)...)).Run(context.Background(), harnessx.Target{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range summary.Results {
		if r.Skipped {
			got[string(r.CheckID)+"/"+r.ResourceID] = r.SkipReason
		}
	}
	want := map[string]string{
		"p.dep/b":  hostSaysNo,
		"p.cond/a": "condition not met",
		"p.cond/b": hostSaysNo,
		"p.skip/a": "pack says no",
		"p.skip/b": hostSaysNo,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("skips = %v, want %v", got, want)
	}
}

func TestContextErrorSkipsPackChecks(t *testing.T) {
	checks := embed.MustChecks(globalPack(), embed.Options{
		Prefix: jwtPrefix,
		Context: func(context.Context, harnessx.Target, *harnessx.Resource) (any, error) {
			return nil, errors.New("no token")
		},
	})
	summary, err := harnessx.New(harnessx.WithChecks(checks...)).Run(context.Background(), harnessx.Target{})
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Results) != 1 || !summary.Results[0].Skipped || !strings.Contains(summary.Results[0].SkipReason, "no token") {
		t.Errorf("results = %+v", summary.Results)
	}
}

func TestRename(t *testing.T) {
	checks := embed.MustChecks(globalPack(), embed.Options{
		Rename: func(id harnessx.CheckID) harnessx.CheckID { return "x." + id + "_v2" },
	})
	if checks[0].ID != "x.baseline_v2" || checks[1].ID != "x.probe_v2" || !reflect.DeepEqual(checks[1].DependsOn, []harnessx.CheckID{"x.baseline_v2"}) {
		t.Errorf("checks = %q %q deps %v", checks[0].ID, checks[1].ID, checks[1].DependsOn)
	}
	if _, err := harnessx.New(harnessx.WithChecks(checks...)).Run(context.Background(), harnessx.Target{}); err != nil {
		t.Fatal(err)
	}
}

func TestContextIDNeedsNamespace(t *testing.T) {
	_, err := embed.Checks(globalPack(), embed.Options{
		Context: func(context.Context, harnessx.Target, *harnessx.Resource) (any, error) { return nil, nil },
	})
	if err == nil {
		t.Fatal("expected an error without a namespace or ContextID")
	}
}

func TestSelect(t *testing.T) {
	pack := []harnessx.Check{{ID: "a"}, {ID: "b", DependsOn: []harnessx.CheckID{"a"}}, {ID: "c"}, {ID: "d", DependsOn: []harnessx.CheckID{"b"}}}
	got, err := embed.Select(pack, "d")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]harnessx.CheckID, 0, len(got))
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if !reflect.DeepEqual(ids, []harnessx.CheckID{"a", "b", "d"}) {
		t.Errorf("Select = %v", ids)
	}
	if _, err := embed.Select(pack, "zz"); !errors.Is(err, harnessx.ErrUnknownCheckID) {
		t.Errorf("unknown ID: err = %v", err)
	}
}
