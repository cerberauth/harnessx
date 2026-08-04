package harnessx

import (
	"context"
	"testing"
)

func TestSkipAlways_NonEmpty(t *testing.T) {
	sd := SkipAlways("not applicable")
	got := sd.Eval(context.Background(), Target{}, NewStaticResultStore())
	if got != "not applicable" {
		t.Errorf("want %q, got %q", "not applicable", got)
	}
}

func TestSkipAlways_EmptyMeansNoSkip(t *testing.T) {
	sd := SkipAlways("")
	got := sd.Eval(context.Background(), Target{}, NewStaticResultStore())
	if got != "" {
		t.Errorf("empty reason should not trigger skip, got %q", got)
	}
}

func TestSkipWhen_ReturnsReasonWhenTrue(t *testing.T) {
	sd := SkipWhen(func(_ context.Context, _ Target, _ ResultStore) string {
		return "condition met"
	})
	got := sd.Eval(context.Background(), Target{}, NewStaticResultStore())
	if got != "condition met" {
		t.Errorf("want %q, got %q", "condition met", got)
	}
}

func TestSkipWhen_ReturnsEmptyWhenFalse(t *testing.T) {
	sd := SkipWhen(func(_ context.Context, _ Target, _ ResultStore) string {
		return ""
	})
	got := sd.Eval(context.Background(), Target{}, NewStaticResultStore())
	if got != "" {
		t.Errorf("want empty string, got %q", got)
	}
}

func TestSkipWhen_ReceivesTargetAndStore(t *testing.T) {
	target := Target{URL: "http://example.com", Host: "example.com"}
	store := NewStaticResultStore(Result{CheckID: testCheckID})

	var gotTarget Target
	var gotResult Result
	var gotOk bool

	sd := SkipWhen(func(_ context.Context, t Target, s ResultStore) string {
		gotTarget = t
		gotResult, gotOk = s.Get(testCheckID)
		return ""
	})
	sd.Eval(context.Background(), target, store)

	if gotTarget.URL != target.URL {
		t.Errorf("target not passed: want URL %q, got %q", target.URL, gotTarget.URL)
	}
	if !gotOk || gotResult.CheckID != testCheckID {
		t.Errorf("store not passed or result missing: gotResult=%+v", gotResult)
	}
}

func TestSkipDecision_NilFn(t *testing.T) {
	sd := SkipDecision{}
	got := sd.Eval(context.Background(), Target{}, NewStaticResultStore())
	if got != "" {
		t.Errorf("nil fn should return empty string, got %q", got)
	}
}

func TestSkipResourceWhen_ReturnsReasonWhenTrue(t *testing.T) {
	sd := SkipResourceWhen(func(_ context.Context, _ Target, res Resource, _ ResultStore) string {
		if res.ID == "r1" {
			return "not applicable to r1"
		}
		return ""
	})

	got := sd.EvalResource(context.Background(), Target{}, Resource{ID: "r1"}, NewStaticResultStore())
	if got != "not applicable to r1" {
		t.Errorf("want %q, got %q", "not applicable to r1", got)
	}

	got = sd.EvalResource(context.Background(), Target{}, Resource{ID: "r2"}, NewStaticResultStore())
	if got != "" {
		t.Errorf("want empty string, got %q", got)
	}
}

func TestSkipDecision_EvalResource_FallsBackToEval(t *testing.T) {
	sd := SkipAlways("check disabled")
	got := sd.EvalResource(context.Background(), Target{}, Resource{ID: "r1"}, NewStaticResultStore())
	if got != "check disabled" {
		t.Errorf("want %q, got %q", "check disabled", got)
	}
}

func TestSkipDecision_EvalResource_NilDecision(t *testing.T) {
	sd := SkipDecision{}
	got := sd.EvalResource(context.Background(), Target{}, Resource{ID: "r1"}, NewStaticResultStore())
	if got != "" {
		t.Errorf("nil decision should return empty string, got %q", got)
	}
}

func TestSkipWhen_ContextPassedThrough(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "marker")

	sd := SkipWhen(func(c context.Context, _ Target, _ ResultStore) string {
		if c.Value(key{}) != "marker" {
			return "context not passed"
		}
		return ""
	})

	got := sd.Eval(ctx, Target{}, NewStaticResultStore())
	if got != "" {
		t.Errorf("unexpected skip reason: %q", got)
	}
}
