package reporters_test

import (
	"context"
	"errors"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/cerberauth/harnessx"
	"github.com/cerberauth/harnessx/reporters"
)

var _ harnessx.Reporter = (*reporters.OTelReporter)(nil)

func newTestOTelReporter(t *testing.T) *reporters.OTelReporter {
	t.Helper()
	tracer := tracenoop.NewTracerProvider().Tracer("test")
	meter := metricnoop.NewMeterProvider().Meter("test")
	r, err := reporters.NewOTelReporter(context.Background(), tracer, meter)
	if err != nil {
		t.Fatalf("NewOTelReporter: %v", err)
	}
	return r
}

func TestNewOTelReporter(t *testing.T) {
	newTestOTelReporter(t)
}

func TestOTelReporter_OnCheckComplete_passed(t *testing.T) {
	r := newTestOTelReporter(t)
	res := harnessx.Resource{ID: "r1"}
	r.OnCheckStart(harnessx.Check{ID: "c"}, harnessx.Target{}, &res)
	r.OnCheckComplete(harnessx.Result{
		CheckID:    "c",
		ResourceID: "r1",
		Duration:   10 * time.Millisecond,
	})
}

func TestOTelReporter_OnCheckComplete_withError(t *testing.T) {
	r := newTestOTelReporter(t)
	r.OnCheckStart(harnessx.Check{ID: "c"}, harnessx.Target{}, nil)
	r.OnCheckComplete(harnessx.Result{
		CheckID:  "c",
		Duration: 5 * time.Millisecond,
		Err:      errors.New("check failed"),
	})
}

func TestOTelReporter_OnCheckComplete_skipped(t *testing.T) {
	r := newTestOTelReporter(t)
	r.OnCheckStart(harnessx.Check{ID: "c"}, harnessx.Target{}, nil)
	r.OnCheckComplete(harnessx.Result{
		CheckID:    "c",
		Skipped:    true,
		SkipReason: "dependency not met",
	})
}

func TestOTelReporter_OnCheckComplete_withObservations(t *testing.T) {
	r := newTestOTelReporter(t)
	r.OnCheckStart(harnessx.Check{ID: "c"}, harnessx.Target{}, nil)
	r.OnCheckComplete(harnessx.Result{
		CheckID:  "c",
		Duration: 20 * time.Millisecond,
		Observations: []harnessx.Observation{
			{CheckID: "c", Title: "missing header"},
			{CheckID: "c", Title: "unexpected response"},
		},
	})
}

func TestOTelReporter_OnCheckComplete_perResource(t *testing.T) {
	r := newTestOTelReporter(t)
	res := harnessx.Resource{ID: "endpoint-1"}
	r.OnCheckStart(harnessx.Check{ID: "c"}, harnessx.Target{}, &res)
	r.OnCheckComplete(harnessx.Result{
		CheckID:    "c",
		ResourceID: "endpoint-1",
		Duration:   8 * time.Millisecond,
	})
}

func TestOTelReporter_OnCheckComplete_orphan(t *testing.T) {
	r := newTestOTelReporter(t)
	r.OnCheckComplete(harnessx.Result{
		CheckID:  "c",
		Duration: 3 * time.Millisecond,
	})
}

func TestOTelReporter_OnScanComplete_success(t *testing.T) {
	r := newTestOTelReporter(t)
	r.OnScanComplete(harnessx.ScanSummary{
		TotalChecks: 3,
		Executed:    2,
		Skipped:     1,
		Duration:    50 * time.Millisecond,
	})
}

func TestOTelReporter_OnScanComplete_withError(t *testing.T) {
	r := newTestOTelReporter(t)
	r.OnScanComplete(harnessx.ScanSummary{
		Duration: 10 * time.Millisecond,
		Err:      errors.New("scan aborted"),
	})
}

func TestOTelReporter_GlobalScopeSpanLifecycle(t *testing.T) {
	r := newTestOTelReporter(t)
	r.OnCheckStart(harnessx.Check{ID: "global"}, harnessx.Target{}, nil)
	r.OnCheckComplete(harnessx.Result{
		CheckID:    "global",
		ResourceID: "",
		Duration:   5 * time.Millisecond,
	})
}

func TestOTelReporter_OnCheckComplete_withAttempts(t *testing.T) {
	const checkID harnessx.CheckID = "alg-none"
	const acceptedVariant = "NONE"

	r := newTestOTelReporter(t)
	r.OnCheckStart(harnessx.Check{ID: checkID}, harnessx.Target{}, nil)
	r.OnCheckComplete(harnessx.Result{
		CheckID:  checkID,
		Duration: 15 * time.Millisecond,
		Observations: []harnessx.Observation{
			{CheckID: checkID, Variant: acceptedVariant, Title: "alg none accepted"},
		},
		Attempts: []harnessx.Attempt{
			{Variant: "none", Duration: 5 * time.Millisecond},
			{Variant: acceptedVariant, Duration: 5 * time.Millisecond, Observations: []harnessx.Observation{
				{CheckID: checkID, Variant: acceptedVariant, Title: "alg none accepted"},
			}},
			{Variant: "None", Duration: 5 * time.Millisecond, Err: errors.New("timeout")},
		},
	})
}

func TestOTelReporter_ConcurrentPerResourceChecks(t *testing.T) {
	r := newTestOTelReporter(t)
	resources := []harnessx.Resource{
		{ID: "r1"}, {ID: "r2"}, {ID: "r3"},
	}
	chk := harnessx.Check{ID: "concurrent"}
	for i := range resources {
		res := resources[i]
		r.OnCheckStart(chk, harnessx.Target{}, &res)
	}
	for _, res := range resources {
		r.OnCheckComplete(harnessx.Result{
			CheckID:    "concurrent",
			ResourceID: res.ID,
			Duration:   2 * time.Millisecond,
		})
	}
}
