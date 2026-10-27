package harnessx

import (
	"runtime"
	"time"
)

const defaultTimeout = 30 * time.Second

type engineConfig struct {
	maxConcurrency         int
	maxResourceConcurrency int
	defaultTimeout         time.Duration
	reporters              []Reporter
	initialChecks          []Check
	resourceMerge          ResourceMergeFunc
}

func defaultConfig() engineConfig {
	cpus := runtime.NumCPU()
	return engineConfig{
		maxConcurrency:         cpus,
		maxResourceConcurrency: cpus,
		defaultTimeout:         defaultTimeout,
	}
}

// Option configures an Engine; see New.
type Option func(*engineConfig)

// WithMaxConcurrency sets the maximum number of checks run concurrently
// within a level of the dependency graph. The limit is per Run/RunScenario
// call, not shared across calls on the same engine — a shared engine-wide
// semaphore would deadlock a reentrant RunScenario call once the outer call
// held every slot. For a global cap, throttle out of band (e.g. a shared
// rate limiter on the HTTP transport) instead.
func WithMaxConcurrency(n int) Option {
	return func(cfg *engineConfig) {
		if n > 0 {
			cfg.maxConcurrency = n
		}
	}
}

// WithMaxResourceConcurrency is like [WithMaxConcurrency] but for
// per-resource check invocations of a ScopePerResource check.
func WithMaxResourceConcurrency(n int) Option {
	return func(cfg *engineConfig) {
		if n > 0 {
			cfg.maxResourceConcurrency = n
		}
	}
}

// WithDefaultTimeout sets the timeout applied to checks that leave
// Check.Timeout at zero. The default is 30 seconds; non-positive values are
// ignored.
func WithDefaultTimeout(d time.Duration) Option {
	return func(cfg *engineConfig) {
		if d > 0 {
			cfg.defaultTimeout = d
		}
	}
}

// WithReporters sets the engine's default reporters, used by Run and by
// RunScenario calls whose Scenario.Reporters is empty.
func WithReporters(reporters ...Reporter) Option {
	return func(cfg *engineConfig) {
		cfg.reporters = reporters
	}
}

// WithChecks registers checks when the engine is created. New cannot return a
// registration error such as a duplicate ID, so Run returns it instead and runs
// nothing; use Engine.Register to see it right away.
func WithChecks(checks ...Check) Option {
	return func(cfg *engineConfig) {
		cfg.initialChecks = append(cfg.initialChecks, checks...)
	}
}

// ResourceMergeFunc decides what to keep when a check discovers a resource whose
// ID is already known: existing is the version held so far, incoming the newly
// discovered one, and the result replaces the held version. The resource stays
// in its original position, and per-resource checks run once per ID.
type ResourceMergeFunc func(existing, incoming Resource) Resource

// KeepFirstResource is the default ResourceMergeFunc: the first resource
// discovered under an ID wins. Checks in the same dependency level run
// concurrently, so which of their versions comes first is not deterministic;
// make one check depend on the other to fix the order.
func KeepFirstResource(existing, _ Resource) Resource { return existing }

// KeepLastResource is a ResourceMergeFunc under which the most recently
// discovered version of a resource wins. As with KeepFirstResource, the order
// between checks of the same dependency level is not deterministic.
func KeepLastResource(_, incoming Resource) Resource { return incoming }

// WithResourceMerge sets how the engine resolves two discoveries of the same
// resource ID; see ResourceMergeFunc. The default is KeepFirstResource. A nil fn
// is ignored.
func WithResourceMerge(fn ResourceMergeFunc) Option {
	return func(cfg *engineConfig) {
		if fn != nil {
			cfg.resourceMerge = fn
		}
	}
}
