package harnessx

import "time"

type Reporter interface {
	OnCheckStart(check Check, target Target)
	OnCheckComplete(result Result)
	OnScanComplete(summary ScanSummary)
}

type ScanSummary struct {
	Target      Target
	TotalChecks int
	Executed    int
	Skipped     int
	Failed      int
	Findings    []Finding
	Results     []Result
	Duration    time.Duration
	Err         error
}

type NoopReporter struct{}

func (NoopReporter) OnCheckStart(Check, Target) {}
func (NoopReporter) OnCheckComplete(Result)     {}
func (NoopReporter) OnScanComplete(ScanSummary) {}
