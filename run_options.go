package harnessx

// runConfig holds per-run check selection, scoped to a single Engine.Run call.
type runConfig struct {
	only    map[CheckID]struct{}
	exclude map[CheckID]struct{}
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

func (cfg runConfig) filter(checks []Check) []Check {
	if len(cfg.only) == 0 && len(cfg.exclude) == 0 {
		return checks
	}
	filtered := make([]Check, 0, len(checks))
	for _, c := range checks {
		if len(cfg.only) > 0 {
			if _, ok := cfg.only[c.ID]; !ok {
				continue
			}
		}
		if _, ok := cfg.exclude[c.ID]; ok {
			continue
		}
		filtered = append(filtered, c)
	}
	return filtered
}
