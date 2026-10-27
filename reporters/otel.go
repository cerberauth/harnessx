package reporters

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/cerberauth/harnessx"
)

type spanKey struct {
	checkID    harnessx.CheckID
	resourceID string
}

// OTelReporter is a harnessx.Reporter that emits an OpenTelemetry span per
// check execution and records check and scan metrics. Build one with
// NewOTelReporter.
//
// The spans form a tree: a "<prefix>.scan" span, started when the scan starts,
// is the parent of one "<prefix>.check" span per check execution. A check that
// is skipped has no span of its own — skips are reported without a preceding
// OnCheckStart — so it is recorded as a "check.skipped" event on the scan span.
type OTelReporter struct {
	baseCtx     context.Context
	tracer      trace.Tracer
	activeSpans sync.Map // spanKey → trace.Span
	prefix      string

	scanMu   sync.Mutex
	scanCtx  context.Context // parent of the check spans; baseCtx outside a scan
	scanSpan trace.Span      // nil outside a scan

	checkDuration      metric.Float64Histogram
	checkCounter       metric.Int64Counter
	observationCounter metric.Int64Counter
	scanDuration       metric.Float64Histogram
}

// OTelOption configures an OTelReporter; see NewOTelReporter.
type OTelOption func(*otelConfig)

type otelConfig struct {
	prefix string
}

// WithPrefix sets the prefix of the metric names, "harnessx" by default, as in
// "harnessx.check.duration".
func WithPrefix(prefix string) OTelOption {
	return func(c *otelConfig) {
		c.prefix = prefix
	}
}

// NewOTelReporter returns an OTelReporter that starts spans with tracer and
// records metrics with meter, both under ctx. It returns an error if a metric
// instrument cannot be created.
//
// Spans are keyed by check and resource, so use one reporter per concurrent
// scan: sharing it between simultaneous runs of the same check can mix up their
// spans.
func NewOTelReporter(ctx context.Context, tracer trace.Tracer, meter metric.Meter, opts ...OTelOption) (*OTelReporter, error) {
	cfg := &otelConfig{prefix: "harnessx"}
	for _, opt := range opts {
		opt(cfg)
	}
	prefix := cfg.prefix

	checkDuration, err := meter.Float64Histogram(
		prefix+".check.duration",
		metric.WithDescription("Duration of check execution in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	checkCounter, err := meter.Int64Counter(
		prefix+".check.executions",
		metric.WithDescription("Number of check executions by status"),
	)
	if err != nil {
		return nil, err
	}

	observationCounter, err := meter.Int64Counter(
		prefix+".observations",
		metric.WithDescription("Number of observations emitted by checks"),
	)
	if err != nil {
		return nil, err
	}

	scanDuration, err := meter.Float64Histogram(
		prefix+".scan.duration",
		metric.WithDescription("Total scan duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	return &OTelReporter{
		baseCtx:            ctx,
		tracer:             tracer,
		prefix:             prefix,
		checkDuration:      checkDuration,
		checkCounter:       checkCounter,
		observationCounter: observationCounter,
		scanDuration:       scanDuration,
	}, nil
}

// OnScanStart starts the scan span, which the check spans become children of.
func (o *OTelReporter) OnScanStart(target harnessx.Target, totalChecks int) {
	attrs := []attribute.KeyValue{attribute.Int("scan.total_checks", totalChecks)}
	if target.URL != "" {
		attrs = append(attrs, attribute.String("target.url", target.URL))
	}
	if target.Host != "" {
		attrs = append(attrs, attribute.String("target.host", target.Host))
	}

	ctx, span := o.tracer.Start(o.baseCtx, o.prefix+".scan", trace.WithAttributes(attrs...))

	o.scanMu.Lock()
	defer o.scanMu.Unlock()
	o.scanCtx, o.scanSpan = ctx, span
}

// checkParent returns the context check spans are started under.
func (o *OTelReporter) checkParent() context.Context {
	o.scanMu.Lock()
	defer o.scanMu.Unlock()
	if o.scanCtx != nil {
		return o.scanCtx
	}
	return o.baseCtx
}

func (o *OTelReporter) OnCheckStart(check harnessx.Check, _ harnessx.Target, resource *harnessx.Resource) {
	var resourceID string
	if resource != nil {
		resourceID = resource.ID
	}

	attrs := []attribute.KeyValue{
		attribute.String("check.id", string(check.ID)),
	}
	if resourceID != "" {
		attrs = append(attrs, attribute.String("resource.id", resourceID))
	}

	_, span := o.tracer.Start(o.checkParent(), o.prefix+".check", trace.WithAttributes(attrs...))
	o.activeSpans.Store(spanKey{check.ID, resourceID}, span)
}

func (o *OTelReporter) OnCheckComplete(result harnessx.Result) {
	key := spanKey{result.CheckID, result.ResourceID}
	v, started := o.activeSpans.LoadAndDelete(key)
	if !started && result.Skipped {
		o.recordSkipEvent(result)
	}
	if started {
		span := v.(trace.Span)

		if result.Skipped {
			span.SetAttributes(attribute.Bool("check.skipped", true))
			if result.SkipReason != "" {
				span.SetAttributes(attribute.String("check.skip_reason", result.SkipReason))
			}
		}

		if len(result.Attempts) > 0 {
			span.SetAttributes(attribute.Int("check.attempts", len(result.Attempts)))
			for _, a := range result.Attempts {
				attrs := []attribute.KeyValue{
					attribute.String("variant", a.Variant),
					attribute.Int("observations", len(a.Observations)),
					attribute.Float64("duration_ms", float64(a.Duration.Microseconds())/1000),
				}
				if a.Err != nil {
					attrs = append(attrs, attribute.String("error", a.Err.Error()))
				}
				span.AddEvent("check.attempt", trace.WithAttributes(attrs...))
			}
		}

		if result.Err != nil {
			span.RecordError(result.Err)
			span.SetStatus(codes.Error, result.Err.Error())
		}

		span.End()
	}

	status := string(result.Status())

	o.checkCounter.Add(o.baseCtx, 1,
		metric.WithAttributes(
			attribute.String("check.id", string(result.CheckID)),
			attribute.String("status", status),
		),
	)
	// A check skipped before it started has no duration; recording a zero would
	// drag the histogram down.
	if started {
		o.checkDuration.Record(o.baseCtx, result.Duration.Seconds(),
			metric.WithAttributes(attribute.String("check.id", string(result.CheckID))),
		)
	}

	if len(result.Observations) > 0 {
		o.observationCounter.Add(o.baseCtx, int64(len(result.Observations)),
			metric.WithAttributes(
				attribute.String("check.id", string(result.CheckID)),
			),
		)
	}
}

// recordSkipEvent adds a "check.skipped" event to the scan span for a check
// that was skipped without ever starting.
func (o *OTelReporter) recordSkipEvent(result harnessx.Result) {
	o.scanMu.Lock()
	span := o.scanSpan
	o.scanMu.Unlock()
	if span == nil {
		return
	}

	attrs := []attribute.KeyValue{attribute.String("check.id", string(result.CheckID))}
	if result.ResourceID != "" {
		attrs = append(attrs, attribute.String("resource.id", result.ResourceID))
	}
	if result.SkipReason != "" {
		attrs = append(attrs, attribute.String("check.skip_reason", result.SkipReason))
	}
	span.AddEvent("check.skipped", trace.WithAttributes(attrs...))
}

// OnScanComplete ends the scan span with the scan's outcome and records its
// duration. A scan that failed before it started, such as one with no checks,
// has no scan span yet; one is created for it so that the failure is visible.
func (o *OTelReporter) OnScanComplete(summary harnessx.ScanSummary) {
	o.scanMu.Lock()
	span := o.scanSpan
	o.scanCtx, o.scanSpan = nil, nil
	o.scanMu.Unlock()

	if span == nil {
		_, span = o.tracer.Start(o.baseCtx, o.prefix+".scan",
			trace.WithTimestamp(time.Now().Add(-summary.Duration)),
			trace.WithAttributes(attribute.Int("scan.total_checks", summary.TotalChecks)),
		)
	}

	span.SetAttributes(
		attribute.Int("scan.executed", summary.Executed),
		attribute.Int("scan.skipped", summary.Skipped),
		attribute.Int("scan.failed", summary.Failed),
		attribute.Int("scan.observations", len(summary.Observations)),
	)
	if summary.Err != nil {
		span.RecordError(summary.Err)
		span.SetStatus(codes.Error, summary.Err.Error())
	}
	span.End()

	o.scanDuration.Record(o.baseCtx, summary.Duration.Seconds())
}
