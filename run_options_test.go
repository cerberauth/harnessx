package harnessx

import (
	"context"
	"sort"
	"testing"
)

func buildRunConfig(opts ...RunOption) runConfig {
	var cfg runConfig
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

func filteredIDs(checks []Check) []string {
	ids := make([]string, len(checks))
	for i, c := range checks {
		ids[i] = string(c.ID)
	}
	sort.Strings(ids)
	return ids
}

func eqIDs(t *testing.T, got []Check, want ...string) {
	t.Helper()
	sort.Strings(want)
	gs := filteredIDs(got)
	if len(gs) != len(want) {
		t.Fatalf("ids: want %v, got %v", want, gs)
	}
	for i := range want {
		if gs[i] != want[i] {
			t.Fatalf("ids: want %v, got %v", want, gs)
		}
	}
}

func metaChecks() []Check {
	return []Check{
		{ID: "low", Run: stubRun, CVSSScore: 3.1, CWEID: "CWE-200", OWASP: "API3:2023"},
		{ID: "high", Run: stubRun, CVSSScore: 9.1, CWEID: "CWE-345", CAPECID: "CAPEC-31", OWASP: "API2:2023"},
		{ID: "dep", Run: stubRun, CVSSScore: 0},
		{ID: "high-with-dep", Run: stubRun, CVSSScore: 8.2, CWEID: "CWE-287", DependsOn: []CheckID{"dep"}},
	}
}

func TestRunConfigFilter_NoOptionsReturnsAll(t *testing.T) {
	checks := metaChecks()
	got := buildRunConfig().filter(checks)
	eqIDs(t, got, "low", "high", "dep", "high-with-dep")
}

func TestRunConfigFilter_MinCVSSScore(t *testing.T) {
	got := buildRunConfig(WithMinCVSSScore(8.0)).filter(metaChecks())
	// "dep" is pulled in as a dependency of "high-with-dep" despite its 0 score.
	eqIDs(t, got, "high", "high-with-dep", "dep")
}

func TestRunConfigFilter_CVSSScoreRange(t *testing.T) {
	got := buildRunConfig(WithCVSSScoreRange(3.0, 8.5)).filter(metaChecks())
	eqIDs(t, got, "low", "high-with-dep", "dep")
}

func TestRunConfigFilter_CWEID(t *testing.T) {
	got := buildRunConfig(WithCWEID("cwe-345", "CWE-287")).filter(metaChecks())
	eqIDs(t, got, "high", "high-with-dep", "dep")
}

func TestRunConfigFilter_OWASPAndCAPEC(t *testing.T) {
	got := buildRunConfig(WithOWASP("API2:2023"), WithCAPECID("CAPEC-31")).filter(metaChecks())
	eqIDs(t, got, "high")
}

func TestRunConfigFilter_ExcludeWinsOverDependencyPull(t *testing.T) {
	got := buildRunConfig(WithMinCVSSScore(8.0), WithExclude("dep")).filter(metaChecks())
	eqIDs(t, got, "high", "high-with-dep")
}

func TestRunConfigFilter_OnlyAndPredicateAreAnded(t *testing.T) {
	got := buildRunConfig(WithOnly("low", "high"), WithMinCVSSScore(8.0)).filter(metaChecks())
	eqIDs(t, got, "high")
}

func TestEngineRun_WithMinCVSSScore_KeepsDependencies(t *testing.T) {
	rep := &testReporter{}
	e := New(WithReporters(rep))
	if err := e.Register(metaChecks()...); err != nil {
		t.Fatalf("register: %v", err)
	}

	summary, err := e.Run(context.Background(), testTarget, WithMinCVSSScore(8.0))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if summary.TotalChecks != 3 {
		t.Fatalf("TotalChecks: want 3 (high, high-with-dep, dep), got %d", summary.TotalChecks)
	}
}
