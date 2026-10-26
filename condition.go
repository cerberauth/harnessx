package harnessx

// IfCheckPassed is true when check id ran, was not skipped, returned no error
// and produced no observations. It is false when no result is recorded for id,
// which includes any ScopePerResource check (see ResultStore).
func IfCheckPassed(id CheckID) Condition {
	return func(store ResultStore) bool {
		r, ok := store.Get(id)
		if !ok {
			return false
		}
		return !r.Skipped && r.Err == nil && len(r.Observations) == 0
	}
}

// IfCheckObserved is true when check id produced at least one observation. It
// is false when no result is recorded for id, which includes any
// ScopePerResource check (see ResultStore).
func IfCheckObserved(id CheckID) Condition {
	return func(store ResultStore) bool {
		r, ok := store.Get(id)
		if !ok {
			return false
		}
		return len(r.Observations) > 0
	}
}

// IfCheckSkipped is true when check id was skipped. It is false when no result
// is recorded for id, which includes any ScopePerResource check (see
// ResultStore).
func IfCheckSkipped(id CheckID) Condition {
	return func(store ResultStore) bool {
		r, ok := store.Get(id)
		if !ok {
			return false
		}
		return r.Skipped
	}
}

// All is true when every condition is true, and for no conditions at all.
func All(conditions ...Condition) Condition {
	return func(store ResultStore) bool {
		for _, c := range conditions {
			if !c(store) {
				return false
			}
		}
		return true
	}
}

// Any is true when at least one condition is true, and false for no conditions.
func Any(conditions ...Condition) Condition {
	return func(store ResultStore) bool {
		for _, c := range conditions {
			if c(store) {
				return true
			}
		}
		return false
	}
}

// Not negates c.
func Not(c Condition) Condition {
	return func(store ResultStore) bool {
		return !c(store)
	}
}
