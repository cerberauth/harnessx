package harnessx

// ResultData returns a Result for check id that carries data.
func ResultData(id CheckID, data any) Result {
	return Result{CheckID: id, Data: data}
}

// DataResult returns a Result carrying data without a CheckID.
// The engine always sets CheckID after the run, so callers inside
// a Run func do not need to supply the ID themselves.
func DataResult(data any) Result {
	return Result{Data: data}
}

// Skip returns a Result for check id that is skipped with reason.
func Skip(id CheckID, reason string) Result {
	return Result{CheckID: id, Skipped: true, SkipReason: reason}
}

// DataAs returns r.Data as a T, and whether it was one.
func DataAs[T any](r Result) (T, bool) {
	v, ok := r.Data.(T)
	return v, ok
}

// GetData returns the Data of the result recorded for check id as a T. It
// reports false if there is no such result or its Data is not a T. A
// ScopePerResource check's aggregate result carries no Data (see ResultStore),
// so read per-resource Data with ResultStore.GetForResource and DataAs.
func GetData[T any](store ResultStore, id CheckID) (T, bool) {
	r, ok := store.Get(id)
	if !ok {
		var zero T
		return zero, false
	}
	return DataAs[T](r)
}

// ResourceDataAs returns resource.Data as a T, and whether it was one.
func ResourceDataAs[T any](resource Resource) (T, bool) {
	v, ok := resource.Data.(T)
	return v, ok
}
