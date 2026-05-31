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

// MultiReporter fans out to multiple reporters.
type MultiReporter struct {
	reporters []Reporter
}

// NewMultiReporter creates a reporter that dispatches to multiple reporters.
func NewMultiReporter(reporters ...Reporter) *MultiReporter {
	return &MultiReporter{reporters: reporters}
}

func (m *MultiReporter) OnCheckStart(check Check, target Target) {
	for _, r := range m.reporters {
		r.OnCheckStart(check, target)
	}
}

func (m *MultiReporter) OnCheckComplete(result Result) {
	for _, r := range m.reporters {
		r.OnCheckComplete(result)
	}
}

func (m *MultiReporter) OnScanComplete(summary ScanSummary) {
	for _, r := range m.reporters {
		r.OnScanComplete(summary)
	}
}
