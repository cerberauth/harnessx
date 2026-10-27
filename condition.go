package harnessx

// IfCheckPassed is true when check id ran, was not skipped, returned no error
// and produced no observations. For a ScopePerResource check that holds across
// all its resources (see ResultStore). It is false when no result is recorded
// for id.
func IfCheckPassed(id CheckID) Condition {
	return func(store ResultStore) bool {
		r, ok := store.Get(id)
		if !ok {
			return false
		}
		return r.Status() == StatusPassed
	}
}

// IfCheckObserved is true when check id produced at least one observation, for
// a ScopePerResource check on any of its resources. It is false when no result
// is recorded for id.
func IfCheckObserved(id CheckID) Condition {
	return func(store ResultStore) bool {
		r, ok := store.Get(id)
		if !ok {
			return false
		}
		return len(r.Observations) > 0
	}
}

// IfCheckSkipped is true when check id was skipped, for a ScopePerResource
// check when every one of its resources was. It is false when no result is
// recorded for id.
func IfCheckSkipped(id CheckID) Condition {
	return func(store ResultStore) bool {
		r, ok := store.Get(id)
		if !ok {
			return false
		}
		return r.Status() == StatusSkipped
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
