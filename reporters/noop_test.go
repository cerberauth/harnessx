package reporters_test

import (
	"testing"

	"github.com/cerberauth/harnessx"
	"github.com/cerberauth/harnessx/reporters"
)

var _ harnessx.Reporter = reporters.NoopReporter{}

func TestNoopReporter_OnCheckStart_global(t *testing.T) {
	r := reporters.NoopReporter{}
	r.OnCheckStart(harnessx.Check{ID: "c"}, harnessx.Target{}, nil)
}

func TestNoopReporter_OnCheckStart_perResource(t *testing.T) {
	r := reporters.NoopReporter{}
	res := harnessx.Resource{ID: "r1"}
	r.OnCheckStart(harnessx.Check{ID: "c"}, harnessx.Target{}, &res)
}

func TestNoopReporter_OnCheckComplete(t *testing.T) {
	r := reporters.NoopReporter{}
	r.OnCheckComplete(harnessx.Result{CheckID: "c"})
}

func TestNoopReporter_OnScanComplete(t *testing.T) {
	r := reporters.NoopReporter{}
	r.OnScanComplete(harnessx.ScanSummary{})
}
