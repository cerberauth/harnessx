package harnessx

import "time"

type Reporter interface {
	// OnScanStart fires once before any check runs. totalChecks is the number
	// of checks that will be scheduled: the count after run-option filtering
	// and dependency closure. A ScopePerResource check counts once here
	// however many resources it fans out over; see ResourcePlanReporter.
	OnScanStart(target Target, totalChecks int)
	OnCheckStart(check Check, target Target, resource *Resource)
	OnCheckComplete(result Result)
	OnScanComplete(summary ScanSummary)
}

// ResourcePlanReporter is an optional Reporter extension. A Reporter that also
// implements it learns how many resources each ScopePerResource check fans out
// over, which together with OnScanStart's totalChecks is enough to size a
// progress bar: OnCheckStart and OnCheckComplete fire once per resource.
//
// OnResourcesDiscovered is called once per such check, just before its first
// per-resource run, with the number of resources it will run over. It is not
// called when the check is skipped for lack of resources. Checks in the same
// dependency level run concurrently, so calls may overlap with each other and
// with other Reporter methods.
type ResourcePlanReporter interface {
	OnResourcesDiscovered(check Check, resources int)
}

type ScanSummary struct {
	Target       Target
	TotalChecks  int
	Executed     int
	Skipped      int
	Failed       int
	Observations []Observation
	Results      []Result
	Duration     time.Duration
	Err          error
}
