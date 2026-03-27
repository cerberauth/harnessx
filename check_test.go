package harnessx

import (
	"context"
	"testing"
)

func TestSkipAlways_NonEmpty(t *testing.T) {
	sd := SkipAlways("not applicable")
	got := sd.eval(context.Background(), Target{}, newStaticStore())
	if got != "not applicable" {
		t.Errorf("want %q, got %q", "not applicable", got)
	}
}

func TestSkipAlways_EmptyMeansNoSkip(t *testing.T) {
	sd := SkipAlways("")
	got := sd.eval(context.Background(), Target{}, newStaticStore())
	if got != "" {
		t.Errorf("empty reason should not trigger skip, got %q", got)
	}
}

func TestSkipWhen_ReturnsReasonWhenTrue(t *testing.T) {
	sd := SkipWhen(func(_ context.Context, _ Target, _ ResultStore) string {
		return "condition met"
	})
	got := sd.eval(context.Background(), Target{}, newStaticStore())
	if got != "condition met" {
		t.Errorf("want %q, got %q", "condition met", got)
	}
}

func TestSkipWhen_ReturnsEmptyWhenFalse(t *testing.T) {
	sd := SkipWhen(func(_ context.Context, _ Target, _ ResultStore) string {
		return ""
	})
	got := sd.eval(context.Background(), Target{}, newStaticStore())
	if got != "" {
		t.Errorf("want empty string, got %q", got)
	}
}

func TestSkipWhen_ReceivesTargetAndStore(t *testing.T) {
	target := Target{URL: "http://example.com", Host: "example.com"}
	store := newStaticStore(Result{CheckID: "dep"})

	var gotTarget Target
	var gotResult Result
	var gotOk bool

	sd := SkipWhen(func(_ context.Context, t Target, s ResultStore) string {
		gotTarget = t
		gotResult, gotOk = s.Get("dep")
		return ""
	})
	sd.eval(context.Background(), target, store)

	if gotTarget.URL != target.URL {
		t.Errorf("target not passed: want URL %q, got %q", target.URL, gotTarget.URL)
	}
	if !gotOk || gotResult.CheckID != "dep" {
		t.Error("store not passed correctly")
	}
}

func TestSkipDecision_NilFn(t *testing.T) {
	sd := SkipDecision{}
	got := sd.eval(context.Background(), Target{}, newStaticStore())
	if got != "" {
		t.Errorf("nil fn should return empty string, got %q", got)
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

	got := sd.eval(ctx, Target{}, newStaticStore())
	if got != "" {
		t.Errorf("unexpected skip reason: %q", got)
	}
}
