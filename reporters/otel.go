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

type OTelReporter struct {
	baseCtx     context.Context
	tracer      trace.Tracer
	activeSpans sync.Map // spanKey → trace.Span
	prefix      string

	checkDuration      metric.Float64Histogram
	checkCounter       metric.Int64Counter
	observationCounter metric.Int64Counter
	scanDuration       metric.Float64Histogram
}

type OTelOption func(*otelConfig)

type otelConfig struct {
	prefix string
}

func WithPrefix(prefix string) OTelOption {
	return func(c *otelConfig) {
		c.prefix = prefix
	}
}

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

	_, span := o.tracer.Start(o.baseCtx, o.prefix+".check", trace.WithAttributes(attrs...))
	o.activeSpans.Store(spanKey{check.ID, resourceID}, span)
}

func (o *OTelReporter) OnCheckComplete(result harnessx.Result) {
	key := spanKey{result.CheckID, result.ResourceID}
	if v, ok := o.activeSpans.LoadAndDelete(key); ok {
		span := v.(trace.Span)

		if result.Skipped {
			span.SetAttributes(attribute.Bool("check.skipped", true))
			if result.SkipReason != "" {
				span.SetAttributes(attribute.String("check.skip_reason", result.SkipReason))
			}
		}

		if result.Err != nil {
			span.RecordError(result.Err)
			span.SetStatus(codes.Error, result.Err.Error())
		}

		span.End()
	}

	status := checkStatus(result)

	o.checkCounter.Add(o.baseCtx, 1,
		metric.WithAttributes(
			attribute.String("check.id", string(result.CheckID)),
			attribute.String("status", status),
		),
	)
	o.checkDuration.Record(o.baseCtx, result.Duration.Seconds(),
		metric.WithAttributes(attribute.String("check.id", string(result.CheckID))),
	)

	if len(result.Observations) > 0 {
		o.observationCounter.Add(o.baseCtx, int64(len(result.Observations)),
			metric.WithAttributes(
				attribute.String("check.id", string(result.CheckID)),
			),
		)
	}
}

func (o *OTelReporter) OnScanComplete(summary harnessx.ScanSummary) {
	startTime := time.Now().Add(-summary.Duration)

	_, span := o.tracer.Start(o.baseCtx, o.prefix+".scan",
		trace.WithTimestamp(startTime),
		trace.WithAttributes(
			attribute.Int("scan.total_checks", summary.TotalChecks),
			attribute.Int("scan.executed", summary.Executed),
			attribute.Int("scan.skipped", summary.Skipped),
			attribute.Int("scan.failed", summary.Failed),
			attribute.Int("scan.observations", len(summary.Observations)),
		),
	)

	if summary.Err != nil {
		span.RecordError(summary.Err)
		span.SetStatus(codes.Error, summary.Err.Error())
	}

	span.End(trace.WithTimestamp(time.Now()))

	o.scanDuration.Record(o.baseCtx, summary.Duration.Seconds())
}

func checkStatus(r harnessx.Result) string {
	switch {
	case r.Skipped:
		return "skipped"
	case r.Err != nil:
		return "error"
	case len(r.Observations) > 0:
		return "failed"
	default:
		return "passed"
	}
}
