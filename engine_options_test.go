package harnessx

import (
	"context"
	"sync"
	"testing"
)

func resourceURLs(t *testing.T, e *Engine) []string {
	t.Helper()
	var (
		mu   sync.Mutex
		urls []string
	)
	if err := e.Register(resourceCheck("each", []CheckID{"d1", "d2"}, func(_ context.Context, _ Target, r Resource, _ ResultStore) (Result, error) {
		mu.Lock()
		defer mu.Unlock()
		urls = append(urls, r.URL)
		return Result{}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Run(context.Background(), Target{}); err != nil {
		t.Fatal(err)
	}
	return urls
}

func TestWithResourceMerge(t *testing.T) {
	// d2 depends on d1, so the order the two versions are discovered in is fixed.
	register := func(e *Engine) {
		t.Helper()
		first := globalCheck("d1", nil, func(context.Context, Target, ResultStore) (Result, error) {
			return Result{Resources: []Resource{{ID: dupResourceID, URL: "v1"}}}, nil
		})
		second := globalCheck("d2", []CheckID{"d1"}, func(context.Context, Target, ResultStore) (Result, error) {
			return Result{Resources: []Resource{{ID: dupResourceID, URL: "v2"}}}, nil
		})
		if err := e.Register(first, second); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		opts []Option
		want string
	}{
		{"default keeps the first", nil, "v1"},
		{"KeepFirstResource", []Option{WithResourceMerge(KeepFirstResource)}, "v1"},
		{"KeepLastResource", []Option{WithResourceMerge(KeepLastResource)}, "v2"},
		{"custom merge", []Option{WithResourceMerge(func(existing, incoming Resource) Resource {
			existing.URL += "+" + incoming.URL
			return existing
		})}, "v1+v2"},
		{"nil is ignored", []Option{WithResourceMerge(nil)}, "v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(tt.opts...)
			register(e)
			got := resourceURLs(t, e)
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("per-resource check saw %v, want exactly one resource with URL %q", got, tt.want)
			}
		})
	}
}

// countingReporter counts completed checks.
type countingReporter struct {
	mu        sync.Mutex
	completed int
}

func (*countingReporter) OnScanStart(Target, int)               {}
func (*countingReporter) OnCheckStart(Check, Target, *Resource) {}
func (*countingReporter) OnScanComplete(ScanSummary)            {}
func (r *countingReporter) OnCheckComplete(Result) {
	r.mu.Lock()
	r.completed++
	r.mu.Unlock()
}

func TestWithRunReporters(t *testing.T) {
	engineWide, perRun, perScenario := &countingReporter{}, &countingReporter{}, &countingReporter{}
	e := New(WithReporters(engineWide), WithChecks(globalCheck("a", nil, stubRun)))

	if _, err := e.Run(context.Background(), Target{}); err != nil {
		t.Fatal(err)
	}
	if engineWide.completed != 1 {
		t.Fatalf("engine reporter saw %d checks, want 1", engineWide.completed)
	}

	if _, err := e.Run(context.Background(), Target{}, WithRunReporters(perRun)); err != nil {
		t.Fatal(err)
	}
	if perRun.completed != 1 || engineWide.completed != 1 {
		t.Errorf("Run with WithRunReporters: per-run = %d, engine = %d; want 1 and no new engine callbacks", perRun.completed, engineWide.completed)
	}

	scenario := Scenario{Checks: []Check{globalCheck("b", nil, stubRun)}, Reporters: []Reporter{perScenario}}
	if _, err := e.RunScenario(context.Background(), Target{}, scenario); err != nil {
		t.Fatal(err)
	}
	if perScenario.completed != 1 {
		t.Errorf("scenario reporter saw %d checks, want 1", perScenario.completed)
	}

	if _, err := e.RunScenario(context.Background(), Target{}, scenario, WithRunReporters(perRun)); err != nil {
		t.Fatal(err)
	}
	if perRun.completed != 2 || perScenario.completed != 1 {
		t.Errorf("WithRunReporters must beat Scenario.Reporters: per-run = %d, scenario = %d; want 2 and 1", perRun.completed, perScenario.completed)
	}
}
