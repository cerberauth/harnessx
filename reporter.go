package harnessx

import "time"

// Reporter receives progress callbacks while a scan runs. Checks of one
// dependency level run concurrently, so OnCheckStart and OnCheckComplete can be
// called from several goroutines at once and implementations must be safe for
// concurrent use. OnScanStart and OnScanComplete are called once per scan.
//
// A skipped check is reported with OnCheckComplete only, with no preceding
// OnCheckStart. A ScopePerResource check is reported once per resource, with
// OnCheckStart's resource set.
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

// ScanSummary is the aggregate outcome of a scan, passed to
// Reporter.OnScanComplete and returned by Engine.Run.
//
// TotalChecks counts scheduled checks, a per-resource check counting once;
// Executed, Skipped and Failed count individual executions, so a per-resource
// check adds one per resource. Results and Observations are in completion
// order, which is not deterministic across concurrent checks.
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
