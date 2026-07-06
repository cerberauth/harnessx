package harnessx

// StaticResultStore is a ResultStore fixture for tests: seed it with the
// Results a check's Skip decision or Conditions should see, without running
// the Engine.
type StaticResultStore struct {
	results map[CheckID]Result
}

func NewStaticResultStore(results ...Result) *StaticResultStore {
	s := &StaticResultStore{results: make(map[CheckID]Result, len(results))}
	for _, r := range results {
		s.results[r.CheckID] = r
	}
	return s
}

func (s *StaticResultStore) Get(id CheckID) (Result, bool) {
	r, ok := s.results[id]
	return r, ok
}

func (s *StaticResultStore) GetForResource(id CheckID, _ string) (Result, bool) {
	r, ok := s.results[id]
	return r, ok
}

func (s *StaticResultStore) Observations() []Observation {
	var out []Observation
	for _, r := range s.results {
		out = append(out, r.Observations...)
	}
	return out
}

func (s *StaticResultStore) Resources() []Resource { return nil }
