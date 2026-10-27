package harnessx

import "slices"

// StaticResultStore is a ResultStore fixture for tests: seed it with the
// Results a check's Skip decision or Conditions should see, without running
// the Engine.
type StaticResultStore struct {
	results   map[CheckID]Result
	perRes    map[checkResourceKey]Result
	resources []Resource
}

// NewStaticResultStore returns a StaticResultStore seeded with results, keyed by
// each Result's CheckID. A Result with a ResourceID is the result of a
// ScopePerResource check for that resource, as GetForResource returns it; Get
// then returns a rolled-up result, like the engine's, unless a Result without a
// ResourceID was seeded for the same check.
func NewStaticResultStore(results ...Result) *StaticResultStore {
	s := &StaticResultStore{
		results: make(map[CheckID]Result, len(results)),
		perRes:  make(map[checkResourceKey]Result),
	}
	for _, r := range results {
		if r.ResourceID != "" {
			s.perRes[checkResourceKey{r.CheckID, r.ResourceID}] = r
			continue
		}
		s.results[r.CheckID] = r
	}
	return s
}

// WithResources seeds the resources returned by Resources, and returns s.
func (s *StaticResultStore) WithResources(resources ...Resource) *StaticResultStore {
	s.resources = append(s.resources, resources...)
	return s
}

// Get returns the result seeded for id without a ResourceID or, failing that,
// the roll-up of the per-resource results seeded for it.
func (s *StaticResultStore) Get(id CheckID) (Result, bool) {
	if r, ok := s.results[id]; ok {
		return r, true
	}
	var perResource []Result
	for _, k := range sortedKeys(s.perRes) {
		if k.checkID == id {
			perResource = append(perResource, s.perRes[k])
		}
	}
	if len(perResource) == 0 {
		return Result{}, false
	}
	ran := make([]bool, len(perResource))
	for i := range ran {
		ran[i] = true
	}
	return aggregateResources(id, perResource, ran, 0), true
}

// GetForResource returns the result seeded for id and resourceID. If none was
// seeded for that resource it falls back to the result seeded for id alone,
// whatever the resource.
func (s *StaticResultStore) GetForResource(id CheckID, resourceID string) (Result, bool) {
	if r, ok := s.perRes[checkResourceKey{id, resourceID}]; ok {
		return r, true
	}
	r, ok := s.results[id]
	return r, ok
}

// Observations returns the observations of all seeded results, ordered by check
// ID and then resource ID.
func (s *StaticResultStore) Observations() []Observation {
	ids := make([]CheckID, 0, len(s.results))
	for id := range s.results {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	var out []Observation
	for _, id := range ids {
		out = append(out, s.results[id].Observations...)
	}
	for _, k := range sortedKeys(s.perRes) {
		out = append(out, s.perRes[k].Observations...)
	}
	return out
}

// Resources returns the resources seeded with WithResources.
func (s *StaticResultStore) Resources() []Resource { return s.resources }
