package reporters

import "github.com/cerberauth/harnessx"

// NoopReporter is a Reporter that discards all events.
type NoopReporter struct{}

// OnScanStart does nothing.
func (NoopReporter) OnScanStart(harnessx.Target, int) {}

// OnCheckStart does nothing.
func (NoopReporter) OnCheckStart(harnessx.Check, harnessx.Target, *harnessx.Resource) {}

// OnCheckComplete does nothing.
func (NoopReporter) OnCheckComplete(harnessx.Result) {}

// OnScanComplete does nothing.
func (NoopReporter) OnScanComplete(harnessx.ScanSummary) {}
