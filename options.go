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
}

func defaultConfig() engineConfig {
	cpus := runtime.NumCPU()
	return engineConfig{
		maxConcurrency:         cpus,
		maxResourceConcurrency: cpus,
		defaultTimeout:         defaultTimeout,
	}
}

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

func WithChecks(checks ...Check) Option {
	return func(cfg *engineConfig) {
		cfg.initialChecks = append(cfg.initialChecks, checks...)
	}
}
