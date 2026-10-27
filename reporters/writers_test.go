package reporters_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cerberauth/harnessx"
	"github.com/cerberauth/harnessx/reporters"
)

const testTargetURL = "https://api.example.com"

func scanWith(t *testing.T, reporter harnessx.Reporter) harnessx.ScanSummary {
	t.Helper()
	engine := harnessx.New(harnessx.WithReporters(reporter))
	err := engine.Register(
		harnessx.Check{
			ID: "discover", Scope: harnessx.ScopeGlobal,
			Run: func(context.Context, harnessx.Target, harnessx.ResultStore) (harnessx.Result, error) {
				return harnessx.Result{Resources: []harnessx.Resource{{ID: "r1"}}}, nil
			},
		},
		harnessx.Check{
			ID: "scan", Scope: harnessx.ScopePerResource, DependsOn: []harnessx.CheckID{"discover"},
			RunResource: func(_ context.Context, _ harnessx.Target, r harnessx.Resource, _ harnessx.ResultStore) (harnessx.Result, error) {
				return harnessx.Result{Observations: []harnessx.Observation{{Title: "open redirect", Description: "on " + r.ID}}}, nil
			},
		},
		harnessx.Check{
			ID: "broken", Scope: harnessx.ScopeGlobal,
			Run: func(context.Context, harnessx.Target, harnessx.ResultStore) (harnessx.Result, error) {
				return harnessx.Result{}, errors.New("boom")
			},
		},
		harnessx.Check{
			ID: "off", Scope: harnessx.ScopeGlobal, Skip: harnessx.SkipAlways("not applicable"),
			Run: func(context.Context, harnessx.Target, harnessx.ResultStore) (harnessx.Result, error) {
				return harnessx.Result{}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := engine.Run(context.Background(), harnessx.Target{URL: testTargetURL})
	if err != nil {
		t.Fatal(err)
	}
	return summary
}

func TestTextReporter(t *testing.T) {
	var buf bytes.Buffer
	r := reporters.NewTextReporter(&buf)
	scanWith(t, r)
	out := buf.String()

	for _, want := range []string{
		"scanning " + testTargetURL + ": 4 checks",
		"PASSED  discover",
		"FAILED  scan [r1]",
		"- open redirect: on r1",
		"ERROR   broken",
		": boom",
		"SKIPPED off: not applicable",
		"done in ",
		"2 executed",
		"1 skipped",
		"1 failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	if r.Err() != nil {
		t.Errorf("Err = %v", r.Err())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriterReporters_RememberFirstWriteErrorAndKeepScanning(t *testing.T) {
	text := reporters.NewTextReporter(failingWriter{})
	if summary := scanWith(t, text); summary.Executed == 0 {
		t.Error("a failing writer must not stop the scan")
	}
	if text.Err() == nil {
		t.Error("TextReporter.Err = nil, want the write error")
	}

	js := reporters.NewJSONReporter(failingWriter{})
	scanWith(t, js)
	if js.Err() == nil {
		t.Error("JSONReporter.Err = nil, want the write error")
	}
}

func TestJSONReporter(t *testing.T) {
	var buf bytes.Buffer
	r := reporters.NewJSONReporter(&buf)
	scanWith(t, r)

	var events []map[string]any
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("line %q is not JSON: %v", sc.Text(), err)
		}
		events = append(events, ev)
	}

	if len(events) != 6 { // scan_start, discover, scan[r1], broken, off, scan_complete
		t.Fatalf("got %d events, want 6: %v", len(events), events)
	}
	if events[0]["event"] != "scan_start" || events[0]["target_url"] != testTargetURL || events[0]["total_checks"] != float64(4) {
		t.Errorf("scan_start = %v", events[0])
	}
	last := events[len(events)-1]
	if last["event"] != "scan_complete" || last["executed"] != float64(2) || last["skipped"] != float64(1) || last["failed"] != float64(1) || last["observation_count"] != float64(1) {
		t.Errorf("scan_complete = %v", last)
	}

	byCheck := map[string]map[string]any{}
	for _, ev := range events[1 : len(events)-1] {
		if ev["event"] != "check_complete" {
			t.Errorf("unexpected event %v", ev)
		}
		byCheck[ev["check_id"].(string)] = ev
	}
	if got := byCheck["scan"]; got["status"] != "failed" || got["resource_id"] != "r1" {
		t.Errorf("scan event = %v", got)
	} else if obs, _ := got["observations"].([]any); len(obs) != 1 || obs[0].(map[string]any)["title"] != "open redirect" {
		t.Errorf("scan observations = %v", got["observations"])
	}
	if got := byCheck["broken"]; got["status"] != "error" || got["error"] != "boom" {
		t.Errorf("broken event = %v", got)
	}
	if got := byCheck["off"]; got["status"] != "skipped" || got["skip_reason"] != "not applicable" {
		t.Errorf("off event = %v", got)
	}
	if _, has := byCheck["off"]["duration_ms"]; has {
		t.Error("a skipped check should not report a duration")
	}
}
