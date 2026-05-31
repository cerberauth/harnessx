package reporters

import "github.com/cerberauth/harnessx"

// NoopReporter is a Reporter that discards all events.
type NoopReporter struct{}

func (NoopReporter) OnCheckStart(harnessx.Check, harnessx.Target, *harnessx.Resource) {}
func (NoopReporter) OnCheckComplete(harnessx.Result)                                  {}
func (NoopReporter) OnScanComplete(harnessx.ScanSummary)                              {}
