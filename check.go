package harnessx

import (
	"context"
	"time"
)

type CheckID string

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

var severityRank = map[Severity]int{
	SeverityInfo:     0,
	SeverityLow:      1,
	SeverityMedium:   2,
	SeverityHigh:     3,
	SeverityCritical: 4,
}

type CheckScope int

const (
	ScopeGlobal      CheckScope = iota
	ScopePerResource
)

type Finding struct {
	CheckID     CheckID
	ResourceID  string
	Title       string
	Description string
	Evidence    string
	Severity    Severity
	Metadata    map[string]string
}

type Result struct {
	CheckID    CheckID
	ResourceID string
	Findings   []Finding
	Resources  []Resource
	Skipped    bool
	Duration   time.Duration
	Metadata   map[string]string
	Err        error
}

type ResultStore interface {
	Get(id CheckID) (Result, bool)
	GetForResource(id CheckID, resourceID string) (Result, bool)
	FindingsBySeverity(min Severity) []Finding
	Resources() []Resource
}

type CheckFunc func(ctx context.Context, target Target, store ResultStore) (Result, error)

type ResourceCheckFunc func(ctx context.Context, target Target, resource Resource, store ResultStore) (Result, error)

type Condition func(store ResultStore) bool

type Check struct {
	ID          CheckID
	Name        string
	Description string
	Tags        []string
	DependsOn   []CheckID
	Conditions  []Condition // AND-evaluated; any false → skip

	Scope       CheckScope
	Run         CheckFunc
	RunResource ResourceCheckFunc

	Timeout     time.Duration
	Concurrency int
}
