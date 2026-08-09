package checkdef

import (
	"context"
	"testing"
	"time"

	"github.com/cerberauth/harnessx"
)

func TestNewCheck(t *testing.T) {
	def := wantTestCheckDef()
	run := func(_ context.Context, _ harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
		return harnessx.Result{}, nil
	}

	check := NewCheck(def, run)

	if check.ID != harnessx.CheckID(def.ID) {
		t.Errorf("ID: want %q, got %q", def.ID, check.ID)
	}
	if check.Name != def.Name {
		t.Errorf("Name: want %q, got %q", def.Name, check.Name)
	}
	if check.Description != def.Description {
		t.Errorf("Description: want %q, got %q", def.Description, check.Description)
	}
	if check.Link != def.Link {
		t.Errorf("Link: want %q, got %q", def.Link, check.Link)
	}
	if len(check.DependsOn) != len(def.DependsOn) {
		t.Fatalf("DependsOn: want %d entries, got %d", len(def.DependsOn), len(check.DependsOn))
	}
	if check.Scope != harnessx.ScopeGlobal {
		t.Errorf("Scope: want ScopeGlobal, got %v", check.Scope)
	}
	if check.Run == nil {
		t.Error("Run: want non-nil")
	}
	if check.RunResource != nil {
		t.Error("RunResource: want nil on a NewCheck")
	}
}

func TestNewCheck_Options(t *testing.T) {
	def := wantTestCheckDef()
	run := func(_ context.Context, _ harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
		return harnessx.Result{}, nil
	}
	skip := harnessx.SkipAlways("disabled")
	cond := harnessx.IfCheckSkipped("baseline")

	check := NewCheck(def, run,
		WithSkip(skip),
		WithConditions(cond),
		WithTimeout(5*time.Second),
	)

	if got := check.Skip.Eval(context.Background(), harnessx.Target{}, harnessx.NewStaticResultStore()); got != "disabled" {
		t.Errorf("Skip: want %q, got %q", "disabled", got)
	}
	if len(check.Conditions) != 1 {
		t.Errorf("Conditions: want 1, got %d", len(check.Conditions))
	}
	if check.Timeout != 5*time.Second {
		t.Errorf("Timeout: want 5s, got %v", check.Timeout)
	}
}

func TestNewResourceCheck(t *testing.T) {
	def := wantTestCheckDef()
	run := func(_ context.Context, _ harnessx.Target, _ harnessx.Resource, _ harnessx.ResultStore) (harnessx.Result, error) {
		return harnessx.Result{}, nil
	}

	check := NewResourceCheck(def, run, WithConcurrency(4))

	if check.Scope != harnessx.ScopePerResource {
		t.Errorf("Scope: want ScopePerResource, got %v", check.Scope)
	}
	if check.RunResource == nil {
		t.Error("RunResource: want non-nil")
	}
	if check.Run != nil {
		t.Error("Run: want nil on a NewResourceCheck")
	}
	if check.Concurrency != 4 {
		t.Errorf("Concurrency: want 4, got %d", check.Concurrency)
	}
}
