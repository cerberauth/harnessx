package harnessx

import "time"

type Reporter interface {
	OnCheckStart(check Check, target Target, resource *Resource)
	OnCheckComplete(result Result)
	OnScanComplete(summary ScanSummary)
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
