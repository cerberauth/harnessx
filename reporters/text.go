package reporters

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/cerberauth/harnessx"
)

// TextReporter is a harnessx.Reporter that writes a human-readable log of a scan
// to an io.Writer: one line per finished check, its observations indented
// beneath it, and a one-line summary at the end. It suits CLI tools and
// examples. It is safe for concurrent use; lines from concurrent checks are
// written whole, in completion order.
type TextReporter struct {
	NoopReporter

	mu  sync.Mutex
	w   io.Writer
	err error
}

// NewTextReporter returns a TextReporter that writes to w.
func NewTextReporter(w io.Writer) *TextReporter {
	return &TextReporter{w: w}
}

// Err returns the first error encountered writing to the underlying writer, or
// nil. A write error never interrupts the scan.
func (r *TextReporter) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// OnScanStart writes a header naming the target and the number of checks.
func (r *TextReporter) OnScanStart(target harnessx.Target, totalChecks int) {
	r.printf("scanning %s: %d checks\n", targetName(target), totalChecks)
}

// OnCheckComplete writes the status line of a finished check and its observations.
func (r *TextReporter) OnCheckComplete(result harnessx.Result) {
	var b strings.Builder
	fmt.Fprintf(&b, "%-7s %s", strings.ToUpper(string(result.Status())), result.CheckID)
	if result.ResourceID != "" {
		fmt.Fprintf(&b, " [%s]", result.ResourceID)
	}
	if !result.Skipped {
		fmt.Fprintf(&b, " (%s)", result.Duration.Round(timeRound))
	}
	switch {
	case result.Err != nil:
		fmt.Fprintf(&b, ": %v", result.Err)
	case result.Skipped && result.SkipReason != "":
		fmt.Fprintf(&b, ": %s", result.SkipReason)
	}
	b.WriteByte('\n')
	for _, o := range result.Observations {
		fmt.Fprintf(&b, "        - %s", o.Title)
		if o.Description != "" {
			fmt.Fprintf(&b, ": %s", o.Description)
		}
		b.WriteByte('\n')
	}
	r.printf("%s", b.String())
}

// OnScanComplete writes the summary line.
func (r *TextReporter) OnScanComplete(summary harnessx.ScanSummary) {
	line := fmt.Sprintf("done in %s: %d executed, %d skipped, %d failed, %d observations",
		summary.Duration.Round(timeRound), summary.Executed, summary.Skipped, summary.Failed, len(summary.Observations))
	if summary.Err != nil {
		line += fmt.Sprintf(" (scan error: %v)", summary.Err)
	}
	r.printf("%s\n", line)
}

func (r *TextReporter) printf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := fmt.Fprintf(r.w, format, args...); err != nil && r.err == nil {
		r.err = err
	}
}

func targetName(t harnessx.Target) string {
	switch {
	case t.URL != "":
		return t.URL
	case t.Host != "":
		return t.Host
	default:
		return "(unnamed target)"
	}
}
