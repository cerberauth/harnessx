package harnessx

func IfCheckPassed(id CheckID) Condition {
	return func(store ResultStore) bool {
		r, ok := store.Get(id)
		if !ok {
			return false
		}
		return !r.Skipped && r.Err == nil && len(r.Findings) == 0
	}
}

func IfCheckFound(id CheckID, min Severity) Condition {
	minRank := severityRank[min]
	return func(store ResultStore) bool {
		r, ok := store.Get(id)
		if !ok {
			return false
		}
		for _, f := range r.Findings {
			if severityRank[f.Severity] >= minRank {
				return true
			}
		}
		return false
	}
}

func IfCheckSkipped(id CheckID) Condition {
	return func(store ResultStore) bool {
		r, ok := store.Get(id)
		if !ok {
			return false
		}
		return r.Skipped
	}
}

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

func Not(c Condition) Condition {
	return func(store ResultStore) bool {
		return !c(store)
	}
}
