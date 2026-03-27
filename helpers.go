package harnessx

func ResultData(id CheckID, data any) Result {
	return Result{CheckID: id, Data: data}
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
