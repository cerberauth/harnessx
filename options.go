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
	reporter               Reporter
	initialChecks          []Check
}

func defaultConfig() engineConfig {
	cpus := runtime.NumCPU()
	return engineConfig{
		maxConcurrency:         cpus,
		maxResourceConcurrency: cpus,
		defaultTimeout:         defaultTimeout,
		reporter:               NoopReporter{},
	}
}

type Option func(*engineConfig)

func WithMaxConcurrency(n int) Option {
	return func(cfg *engineConfig) {
		if n > 0 {
			cfg.maxConcurrency = n
		}
	}
}

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

func WithReporter(r Reporter) Option {
	return func(cfg *engineConfig) {
		if r != nil {
			cfg.reporter = r
		}
	}
}

func WithChecks(checks ...Check) Option {
	return func(cfg *engineConfig) {
		cfg.initialChecks = append(cfg.initialChecks, checks...)
	}
}
