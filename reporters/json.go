package reporters

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/cerberauth/harnessx"
)

const timeRound = time.Millisecond

// JSONReporter is a harnessx.Reporter that writes a scan as JSON Lines: one
// JSON object per line, for scan_start, each check_complete and scan_complete,
// discriminated by its "event" field. It suits piping a scan into other tools.
// It is safe for concurrent use; check_complete lines are in completion order.
type JSONReporter struct {
	NoopReporter

	mu  sync.Mutex
	enc *json.Encoder
	err error
}

// NewJSONReporter returns a JSONReporter that writes to w.
func NewJSONReporter(w io.Writer) *JSONReporter {
	return &JSONReporter{enc: json.NewEncoder(w)}
}

// Err returns the first error encountered encoding or writing an event, or nil.
// An error never interrupts the scan.
func (r *JSONReporter) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

type jsonObservation struct {
	CheckID     string            `json:"check_id,omitempty"`
	ResourceID  string            `json:"resource_id,omitempty"`
	Variant     string            `json:"variant,omitempty"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Evidence    string            `json:"evidence,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type jsonEvent struct {
	Event string `json:"event"`

	// scan_start and scan_complete
	TargetURL   string `json:"target_url,omitempty"`
	TargetHost  string `json:"target_host,omitempty"`
	TotalChecks *int   `json:"total_checks,omitempty"`

	// check_complete
	CheckID      string            `json:"check_id,omitempty"`
	ResourceID   string            `json:"resource_id,omitempty"`
	Status       string            `json:"status,omitempty"`
	SkipReason   string            `json:"skip_reason,omitempty"`
	Observations []jsonObservation `json:"observations,omitempty"`

	// scan_complete
	Executed         *int `json:"executed,omitempty"`
	Skipped          *int `json:"skipped,omitempty"`
	Failed           *int `json:"failed,omitempty"`
	ObservationCount *int `json:"observation_count,omitempty"`

	DurationMS *float64 `json:"duration_ms,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// OnScanStart writes a scan_start event.
func (r *JSONReporter) OnScanStart(target harnessx.Target, totalChecks int) {
	r.emit(jsonEvent{Event: "scan_start", TargetURL: target.URL, TargetHost: target.Host, TotalChecks: &totalChecks})
}

// OnCheckComplete writes a check_complete event.
func (r *JSONReporter) OnCheckComplete(result harnessx.Result) {
	ev := jsonEvent{
		Event:      "check_complete",
		CheckID:    string(result.CheckID),
		ResourceID: result.ResourceID,
		Status:     string(result.Status()),
		SkipReason: result.SkipReason,
	}
	if !result.Skipped {
		ev.DurationMS = durationMS(result.Duration)
	}
	if result.Err != nil {
		ev.Error = result.Err.Error()
	}
	for _, o := range result.Observations {
		ev.Observations = append(ev.Observations, jsonObservation{
			CheckID:     string(o.CheckID),
			ResourceID:  o.ResourceID,
			Variant:     o.Variant,
			Title:       o.Title,
			Description: o.Description,
			Evidence:    o.Evidence,
			Metadata:    o.Metadata,
		})
	}
	r.emit(ev)
}

// OnScanComplete writes a scan_complete event with the totals.
func (r *JSONReporter) OnScanComplete(summary harnessx.ScanSummary) {
	observations := len(summary.Observations)
	ev := jsonEvent{
		Event:            "scan_complete",
		TargetURL:        summary.Target.URL,
		TargetHost:       summary.Target.Host,
		TotalChecks:      &summary.TotalChecks,
		Executed:         &summary.Executed,
		Skipped:          &summary.Skipped,
		Failed:           &summary.Failed,
		ObservationCount: &observations,
		DurationMS:       durationMS(summary.Duration),
	}
	if summary.Err != nil {
		ev.Error = summary.Err.Error()
	}
	r.emit(ev)
}

func (r *JSONReporter) emit(ev jsonEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.enc.Encode(ev); err != nil && r.err == nil {
		r.err = err
	}
}

func durationMS(d time.Duration) *float64 {
	ms := float64(d.Microseconds()) / 1000
	return &ms
}
