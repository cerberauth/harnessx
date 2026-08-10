package checkdef

import (
	"time"

	"github.com/cerberauth/harnessx"
)

type checkConfig struct {
	skip        harnessx.SkipDecision
	conditions  []harnessx.Condition
	timeout     time.Duration
	concurrency int
	variants    []string
	variantMode harnessx.VariantMode
}

// Option configures the optional fields of a Check built by NewCheck or
// NewResourceCheck — everything on harnessx.Check that isn't already
// carried by CheckDef or the required Run/RunResource function.
type Option func(*checkConfig)

// WithSkip sets the check's SkipDecision.
func WithSkip(skip harnessx.SkipDecision) Option {
	return func(cfg *checkConfig) {
		cfg.skip = skip
	}
}

// WithConditions sets the check's Conditions (AND-evaluated).
func WithConditions(conditions ...harnessx.Condition) Option {
	return func(cfg *checkConfig) {
		cfg.conditions = conditions
	}
}

// WithTimeout sets the check's Timeout.
func WithTimeout(d time.Duration) Option {
	return func(cfg *checkConfig) {
		cfg.timeout = d
	}
}

// WithConcurrency sets the check's per-resource Concurrency. Only
// meaningful on a NewResourceCheck.
func WithConcurrency(n int) Option {
	return func(cfg *checkConfig) {
		cfg.concurrency = n
	}
}

// WithVariants sets the check's Variants — same check definition, run once
// per variant (e.g. the different casings of a JWT "alg: none" attack).
func WithVariants(variants ...string) Option {
	return func(cfg *checkConfig) {
		cfg.variants = variants
	}
}

// WithVariantMode sets how Variants are executed: VariantsSequential
// (default) or VariantsParallel.
func WithVariantMode(mode harnessx.VariantMode) Option {
	return func(cfg *checkConfig) {
		cfg.variantMode = mode
	}
}

func buildBase(def CheckDef, opts []Option) harnessx.Check {
	var cfg checkConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return harnessx.Check{
		ID:          harnessx.CheckID(def.ID),
		Name:        def.Name,
		Description: def.Description,
		Link:        def.Link,
		Tags:        def.Tags,
		DependsOn:   def.DependsOnIDs(),
		Skip:        cfg.skip,
		Conditions:  cfg.conditions,
		Timeout:     cfg.timeout,
		Concurrency: cfg.concurrency,
		Variants:    cfg.variants,
		VariantMode: cfg.variantMode,
	}
}

// NewCheck builds a ScopeGlobal harnessx.Check from a CheckDef's metadata
// plus a run function, so individual checks only need to supply what makes
// them different.
func NewCheck(def CheckDef, run harnessx.CheckFunc, opts ...Option) harnessx.Check {
	check := buildBase(def, opts)
	check.Scope = harnessx.ScopeGlobal
	check.Run = run
	return check
}

// NewResourceCheck builds a ScopePerResource harnessx.Check from a
// CheckDef's metadata plus a resource run function.
func NewResourceCheck(def CheckDef, run harnessx.ResourceCheckFunc, opts ...Option) harnessx.Check {
	check := buildBase(def, opts)
	check.Scope = harnessx.ScopePerResource
	check.RunResource = run
	return check
}

// NewVariantCheck builds a ScopeGlobal harnessx.Check whose run function is
// invoked once per entry in WithVariants — the same check definition
// probed with different attempt variants (see VariantMode).
func NewVariantCheck(def CheckDef, run harnessx.VariantCheckFunc, opts ...Option) harnessx.Check {
	check := buildBase(def, opts)
	check.Scope = harnessx.ScopeGlobal
	check.RunVariant = run
	return check
}

// NewVariantResourceCheck builds a ScopePerResource harnessx.Check whose
// resource run function is invoked once per entry in WithVariants, for
// each discovered resource.
func NewVariantResourceCheck(def CheckDef, run harnessx.VariantResourceCheckFunc, opts ...Option) harnessx.Check {
	check := buildBase(def, opts)
	check.Scope = harnessx.ScopePerResource
	check.RunResourceVariant = run
	return check
}
