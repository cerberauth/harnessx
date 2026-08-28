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
	if check.CVSSVector != def.CVSSVector || check.CVSSScore != def.CVSSScore {
		t.Errorf("CVSS: want %q/%v, got %q/%v", def.CVSSVector, def.CVSSScore, check.CVSSVector, check.CVSSScore)
	}
	if check.CWEID != def.CWEID || check.CAPECID != def.CAPECID || check.OWASP != def.OWASP {
		t.Errorf("CWE/CAPEC/OWASP: want %q/%q/%q, got %q/%q/%q",
			def.CWEID, def.CAPECID, def.OWASP, check.CWEID, check.CAPECID, check.OWASP)
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

func TestNewVariantCheck(t *testing.T) {
	def := wantTestCheckDef()
	run := func(_ context.Context, _ harnessx.Target, _ string, _ harnessx.ResultStore) (harnessx.Result, error) {
		return harnessx.Result{}, nil
	}

	check := NewVariantCheck(def, run, WithVariants("none", "NONE", "None"))

	if check.Scope != harnessx.ScopeGlobal {
		t.Errorf("Scope: want ScopeGlobal, got %v", check.Scope)
	}
	if check.RunVariant == nil {
		t.Error("RunVariant: want non-nil")
	}
	if check.Run != nil {
		t.Error("Run: want nil on a NewVariantCheck")
	}
	if len(check.Variants) != 3 {
		t.Fatalf("Variants: want 3, got %d", len(check.Variants))
	}
	if check.VariantMode != harnessx.VariantsSequential {
		t.Errorf("VariantMode: want VariantsSequential (default), got %v", check.VariantMode)
	}
}

func TestNewVariantCheck_ParallelMode(t *testing.T) {
	def := wantTestCheckDef()
	run := func(_ context.Context, _ harnessx.Target, _ string, _ harnessx.ResultStore) (harnessx.Result, error) {
		return harnessx.Result{}, nil
	}

	check := NewVariantCheck(def, run,
		WithVariants("none", "NONE"),
		WithVariantMode(harnessx.VariantsParallel),
	)

	if check.VariantMode != harnessx.VariantsParallel {
		t.Errorf("VariantMode: want VariantsParallel, got %v", check.VariantMode)
	}
}

func TestNewVariantResourceCheck(t *testing.T) {
	def := wantTestCheckDef()
	run := func(_ context.Context, _ harnessx.Target, _ harnessx.Resource, _ string, _ harnessx.ResultStore) (harnessx.Result, error) {
		return harnessx.Result{}, nil
	}

	check := NewVariantResourceCheck(def, run, WithVariants("v1", "v2"), WithConcurrency(4))

	if check.Scope != harnessx.ScopePerResource {
		t.Errorf("Scope: want ScopePerResource, got %v", check.Scope)
	}
	if check.RunResourceVariant == nil {
		t.Error("RunResourceVariant: want non-nil")
	}
	if check.RunResource != nil {
		t.Error("RunResource: want nil on a NewVariantResourceCheck")
	}
	if len(check.Variants) != 2 {
		t.Fatalf("Variants: want 2, got %d", len(check.Variants))
	}
	if check.Concurrency != 4 {
		t.Errorf("Concurrency: want 4, got %d", check.Concurrency)
	}
}
