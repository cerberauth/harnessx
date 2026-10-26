package reporters_test

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/cerberauth/harnessx"
	"github.com/cerberauth/harnessx/reporters"
)

// recSpan records what the reporter does to a span.
type recSpan struct {
	tracenoop.Span
	name   string
	parent string
	attrs  map[string]attribute.Value
	events []recEvent
	ended  bool
}

type recEvent struct {
	name  string
	attrs map[string]attribute.Value
}

func attrMap(kvs []attribute.KeyValue) map[string]attribute.Value {
	m := make(map[string]attribute.Value, len(kvs))
	for _, kv := range kvs {
		m[string(kv.Key)] = kv.Value
	}
	return m
}

func (s *recSpan) SetAttributes(kvs ...attribute.KeyValue) {
	for k, v := range attrMap(kvs) {
		s.attrs[k] = v
	}
}
func (s *recSpan) AddEvent(name string, opts ...trace.EventOption) {
	cfg := trace.NewEventConfig(opts...)
	s.events = append(s.events, recEvent{name: name, attrs: attrMap(cfg.Attributes())})
}
func (s *recSpan) SetStatus(codes.Code, string)            {}
func (s *recSpan) RecordError(error, ...trace.EventOption) {}
func (s *recSpan) End(...trace.SpanEndOption)              { s.ended = true }

// recTracer records every span started, and the span each was started under.
type recTracer struct {
	tracenoop.Tracer
	mu    sync.Mutex
	spans []*recSpan
}

func (t *recTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	cfg := trace.NewSpanStartConfig(opts...)
	s := &recSpan{name: name, attrs: attrMap(cfg.Attributes())}
	if parent, ok := trace.SpanFromContext(ctx).(*recSpan); ok {
		s.parent = parent.name
	}
	t.mu.Lock()
	t.spans = append(t.spans, s)
	t.mu.Unlock()
	return trace.ContextWithSpan(ctx, s), s
}

func (t *recTracer) named(name string) []*recSpan {
	var out []*recSpan
	for _, s := range t.spans {
		if s.name == name {
			out = append(out, s)
		}
	}
	return out
}

func TestOTelReporter_SpanTree(t *testing.T) {
	tracer := &recTracer{}
	r, err := reporters.NewOTelReporter(context.Background(), tracer, metricnoop.NewMeterProvider().Meter("test"))
	if err != nil {
		t.Fatal(err)
	}

	r.OnScanStart(harnessx.Target{URL: testTargetURL, Host: "api.example.com"}, 3)
	r.OnCheckStart(harnessx.Check{ID: "c1"}, harnessx.Target{}, nil)
	r.OnCheckComplete(harnessx.Result{CheckID: "c1"})
	r.OnCheckComplete(harnessx.Result{CheckID: "c2", ResourceID: "r9", Skipped: true, SkipReason: "no auth"})
	r.OnScanComplete(harnessx.ScanSummary{TotalChecks: 3, Executed: 1, Skipped: 1})

	scans := tracer.named("harnessx.scan")
	if len(scans) != 1 {
		t.Fatalf("scan spans = %d, want exactly 1", len(scans))
	}
	scan := scans[0]
	if !scan.ended {
		t.Error("scan span was never ended")
	}
	if scan.attrs["target.url"].AsString() != testTargetURL || scan.attrs["target.host"].AsString() != "api.example.com" {
		t.Errorf("scan span attributes = %v, want the target", scan.attrs)
	}
	if scan.attrs["scan.total_checks"].AsInt64() != 3 || scan.attrs["scan.executed"].AsInt64() != 1 || scan.attrs["scan.skipped"].AsInt64() != 1 {
		t.Errorf("scan span attributes = %v, want totals from start and completion", scan.attrs)
	}

	checks := tracer.named("harnessx.check")
	if len(checks) != 1 || checks[0].parent != "harnessx.scan" {
		t.Fatalf("check spans = %+v, want one child of the scan span", checks)
	}

	if len(tracer.named("harnessx.check")) != 1 {
		t.Error("a skipped check must not get a span of its own")
	}
	if len(scan.events) != 1 || scan.events[0].name != "check.skipped" ||
		scan.events[0].attrs["check.id"].AsString() != "c2" ||
		scan.events[0].attrs["resource.id"].AsString() != "r9" ||
		scan.events[0].attrs["check.skip_reason"].AsString() != "no auth" {
		t.Errorf("scan events = %+v, want one check.skipped event describing c2", scan.events)
	}
}

func TestOTelReporter_ScanSpanForScanThatNeverStarted(t *testing.T) {
	tracer := &recTracer{}
	r, err := reporters.NewOTelReporter(context.Background(), tracer, metricnoop.NewMeterProvider().Meter("test"))
	if err != nil {
		t.Fatal(err)
	}

	r.OnScanComplete(harnessx.ScanSummary{Err: harnessx.ErrNoChecks})

	scans := tracer.named("harnessx.scan")
	if len(scans) != 1 || !scans[0].ended {
		t.Errorf("scan spans = %+v, want one ended span for the failed scan", scans)
	}
}

func TestOTelReporter_ReusableAcrossSequentialScans(t *testing.T) {
	tracer := &recTracer{}
	r, err := reporters.NewOTelReporter(context.Background(), tracer, metricnoop.NewMeterProvider().Meter("test"))
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		r.OnScanStart(harnessx.Target{}, 1)
		r.OnCheckStart(harnessx.Check{ID: "c"}, harnessx.Target{}, nil)
		r.OnCheckComplete(harnessx.Result{CheckID: "c"})
		r.OnScanComplete(harnessx.ScanSummary{TotalChecks: 1, Executed: 1})
	}

	if got := len(tracer.named("harnessx.scan")); got != 2 {
		t.Errorf("scan spans = %d, want one per scan", got)
	}
	for _, c := range tracer.named("harnessx.check") {
		if c.parent != "harnessx.scan" {
			t.Errorf("check span parent = %q", c.parent)
		}
	}
}
