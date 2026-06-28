package harnessx

func ResultData(id CheckID, data any) Result {
	return Result{CheckID: id, Data: data}
}

// DataResult returns a Result carrying data without a CheckID.
// The engine always sets CheckID after the run, so callers inside
// a Run func do not need to supply the ID themselves.
func DataResult(data any) Result {
	return Result{Data: data}
}

func Skip(id CheckID, reason string) Result {
	return Result{CheckID: id, Skipped: true, SkipReason: reason}
}

func DataAs[T any](r Result) (T, bool) {
	v, ok := r.Data.(T)
	return v, ok
}

func GetData[T any](store ResultStore, id CheckID) (T, bool) {
	r, ok := store.Get(id)
	if !ok {
		var zero T
		return zero, false
	}
	return DataAs[T](r)
}

func ResourceDataAs[T any](resource Resource) (T, bool) {
	v, ok := resource.Data.(T)
	return v, ok
}
