package harnessx

import (
	"context"
	"time"
)

type SkipDecision struct {
	fn func(ctx context.Context, target Target, store ResultStore) string
}

func SkipAlways(reason string) SkipDecision {
	return SkipDecision{fn: func(_ context.Context, _ Target, _ ResultStore) string {
		return reason
	}}
}

func SkipWhen(fn func(ctx context.Context, target Target, store ResultStore) string) SkipDecision {
	return SkipDecision{fn: fn}
}

// Eval runs the skip decision and returns a non-empty skip reason if the
// check should be skipped, or "" if it should run.
func (s SkipDecision) Eval(ctx context.Context, target Target, store ResultStore) string {
	if s.fn == nil {
		return ""
	}
	return s.fn(ctx, target, store)
}

type CheckID string

type CheckScope int

const (
	ScopeGlobal CheckScope = iota
	ScopePerResource
)

type Observation struct {
	CheckID     CheckID
	ResourceID  string
	Title       string
	Description string
	Evidence    string
	Metadata    map[string]string
}

type Result struct {
	CheckID      CheckID
	ResourceID   string
	Observations []Observation
	Resources    []Resource
	Skipped      bool
	SkipReason   string
	Duration     time.Duration
	Metadata     map[string]string
	Data         any
	Err          error
}

type ResultStore interface {
	Get(id CheckID) (Result, bool)
	GetForResource(id CheckID, resourceID string) (Result, bool)
	Observations() []Observation
	Resources() []Resource
}

type CheckFunc func(ctx context.Context, target Target, store ResultStore) (Result, error)

type ResourceCheckFunc func(ctx context.Context, target Target, resource Resource, store ResultStore) (Result, error)

type Condition func(store ResultStore) bool

type Check struct {
	ID          CheckID
	Name        string
	Description string
	Link        string
	Tags        []string
	DependsOn   []CheckID
	Conditions  []Condition

	Skip SkipDecision

	Scope       CheckScope
	Run         CheckFunc
	RunResource ResourceCheckFunc

	Timeout     time.Duration
	Concurrency int
}
