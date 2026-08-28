package harnessx

import "strings"

// runConfig holds per-run check selection, scoped to a single Engine.Run call.
type runConfig struct {
	only       map[CheckID]struct{}
	exclude    map[CheckID]struct{}
	predicates []func(Check) bool
}

// RunOption customizes which registered checks execute for a single Engine.Run call.
type RunOption func(*runConfig)

// WithOnly restricts a run to the given check IDs. Combine with WithExclude to
// further narrow the set; excluded IDs win over included ones.
func WithOnly(ids ...CheckID) RunOption {
	return func(cfg *runConfig) {
		if cfg.only == nil {
			cfg.only = make(map[CheckID]struct{}, len(ids))
		}
		for _, id := range ids {
			cfg.only[id] = struct{}{}
		}
	}
}

// WithExclude removes the given check IDs from a run.
func WithExclude(ids ...CheckID) RunOption {
	return func(cfg *runConfig) {
		if cfg.exclude == nil {
			cfg.exclude = make(map[CheckID]struct{}, len(ids))
		}
		for _, id := range ids {
			cfg.exclude[id] = struct{}{}
		}
	}
}

// WithFilter restricts a run to checks for which keep returns true. Multiple
// WithFilter options (and the WithCVSS*/WithCWEID/WithCAPECID/WithOWASP
// helpers, which are built on it) are AND-combined. A check that a selected
// check depends on is kept even when it fails the filter, so the dependency
// graph stays valid — only WithExclude drops a dependency.
func WithFilter(keep func(Check) bool) RunOption {
	return func(cfg *runConfig) {
		cfg.predicates = append(cfg.predicates, keep)
	}
}

// WithMinCVSSScore keeps only checks whose CVSSScore is >= min.
func WithMinCVSSScore(min float64) RunOption {
	return WithFilter(func(c Check) bool { return c.CVSSScore >= min })
}

// WithMaxCVSSScore keeps only checks whose CVSSScore is <= max.
func WithMaxCVSSScore(max float64) RunOption {
	return WithFilter(func(c Check) bool { return c.CVSSScore <= max })
}

// WithCVSSScoreRange keeps only checks whose CVSSScore is within [min, max].
func WithCVSSScoreRange(min, max float64) RunOption {
	return WithFilter(func(c Check) bool { return c.CVSSScore >= min && c.CVSSScore <= max })
}

// WithCVSSVector keeps only checks whose CVSSVector exactly matches one of
// the given vectors.
func WithCVSSVector(vectors ...string) RunOption {
	return WithFilter(func(c Check) bool { return inFold(c.CVSSVector, vectors) })
}

// WithCWEID keeps only checks whose CWEID matches one of the given IDs
// (case-insensitive, e.g. "CWE-345").
func WithCWEID(ids ...string) RunOption {
	return WithFilter(func(c Check) bool { return inFold(c.CWEID, ids) })
}

// WithCAPECID keeps only checks whose CAPECID matches one of the given IDs
// (case-insensitive, e.g. "CAPEC-31").
func WithCAPECID(ids ...string) RunOption {
	return WithFilter(func(c Check) bool { return inFold(c.CAPECID, ids) })
}

// WithOWASP keeps only checks whose OWASP identifier matches one of the
// given values (case-insensitive, e.g. "API2:2023" or "A01:2021").
func WithOWASP(ids ...string) RunOption {
	return WithFilter(func(c Check) bool { return inFold(c.OWASP, ids) })
}

func inFold(v string, set []string) bool {
	for _, s := range set {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func (cfg runConfig) matches(c Check) bool {
	for _, p := range cfg.predicates {
		if !p(c) {
			return false
		}
	}
	return true
}

func (cfg runConfig) filter(checks []Check) []Check {
	if len(cfg.only) == 0 && len(cfg.exclude) == 0 && len(cfg.predicates) == 0 {
		return checks
	}

	byID := make(map[CheckID]Check, len(checks))
	for _, c := range checks {
		byID[c.ID] = c
	}

	selected := make(map[CheckID]struct{}, len(checks))
	for _, c := range checks {
		if _, ok := cfg.exclude[c.ID]; ok {
			continue
		}
		if len(cfg.only) > 0 {
			if _, ok := cfg.only[c.ID]; !ok {
				continue
			}
		}
		if !cfg.matches(c) {
			continue
		}
		selected[c.ID] = struct{}{}
	}

	// Pull in the transitive dependencies of every selected check, even
	// when they don't match only/predicates — otherwise newGraph fails
	// with ErrUnknownDependency. WithExclude still wins: an explicitly
	// excluded ID is never re-added.
	queue := make([]CheckID, 0, len(selected))
	for id := range selected {
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		c, ok := byID[id]
		if !ok {
			continue
		}
		for _, dep := range c.DependsOn {
			if _, excluded := cfg.exclude[dep]; excluded {
				continue
			}
			if _, has := selected[dep]; has {
				continue
			}
			selected[dep] = struct{}{}
			queue = append(queue, dep)
		}
	}

	filtered := make([]Check, 0, len(selected))
	for _, c := range checks {
		if _, ok := selected[c.ID]; ok {
			filtered = append(filtered, c)
		}
	}
	return filtered
}
