package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/cerberauth/harnessx"
	"github.com/cerberauth/harnessx/probe"
)

const checkIDDiscovery = "discovery"

// newVulnerableAPI simulates a small API with a real-world authz bug:
// /admin should require a valid bearer token, but a debug header meant for
// internal tooling bypasses authentication entirely. /reports behaves
// correctly.
func newVulnerableAPI() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Debug-Override") == "true" {
			// Bug: the debug override bypasses auth entirely.
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Header.Get("Authorization") == "Bearer valid-token" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})

	mux.HandleFunc("/reports", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer valid-token" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})

	return httptest.NewServer(mux)
}

// bypassComparator flags the specific "went from denied to allowed" pattern,
// instead of any status code change — an override of the default comparator.
func bypassComparator(baseline, current harnessx.Snapshot) []harnessx.Observation {
	if baseline.StatusCode >= 400 && current.StatusCode >= 200 && current.StatusCode < 300 {
		return []harnessx.Observation{{
			Title: "Authorization bypass",
			Description: fmt.Sprintf(
				"baseline required auth (status %d); request with X-Debug-Override succeeded with status %d",
				baseline.StatusCode, current.StatusCode),
		}}
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running scan: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	srv := newVulnerableAPI()
	defer srv.Close()

	client := probe.New(probe.WithMaxRetries(0)).Client()

	// capture performs an HTTP request and reduces it to a harnessx.Snapshot,
	// keeping the full response (headers, body, duration) for comparators
	// that need more than the status code.
	capture := func(ctx context.Context, req *http.Request) (harnessx.Snapshot, error) {
		statusCode, header, body, duration, err := probe.Do(ctx, client, req)
		if err != nil {
			return harnessx.Snapshot{}, err
		}
		return harnessx.Snapshot{StatusCode: statusCode, Header: header, Body: body, Duration: duration}, nil
	}

	target := harnessx.Target{URL: srv.URL, Host: "local-test-api"}

	// Discovery attaches a manual baseline to admin-endpoint (we already know
	// it should require auth); reports-endpoint gets no manual baseline, so
	// its baseline is captured at runtime instead.
	discovery := harnessx.Check{
		ID:    checkIDDiscovery,
		Name:  "Endpoint Discovery",
		Scope: harnessx.ScopeGlobal,
		Run: func(_ context.Context, t harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
			return harnessx.Result{
				Resources: []harnessx.Resource{
					{ID: "admin-endpoint", URL: t.URL + "/admin", Data: harnessx.Baseline{StatusCode: http.StatusUnauthorized}},
					{ID: "reports-endpoint", URL: t.URL + "/reports"},
				},
			}, nil
		},
	}

	// The "baseline probe": captures the normal, unauthenticated response for
	// every resource and stores it for BaselineFromCheck to read back.
	baselineProbe := harnessx.CaptureBaselineCheck("baseline-probe", "Baseline Probe",
		func(ctx context.Context, _ harnessx.Target, r harnessx.Resource, _ harnessx.ResultStore) (harnessx.Snapshot, error) {
			req, err := harnessx.NewRequestFromResource(ctx, r)
			if err != nil {
				return harnessx.Snapshot{}, err
			}
			return capture(ctx, req)
		})
	baselineProbe.DependsOn = []harnessx.CheckID{checkIDDiscovery}

	// reports-deviation: baseline from the probe, default (status-code) comparator.
	reportsCheck := harnessx.NewBaselineCheck(harnessx.BaselineCheckConfig{
		ID:        "reports-deviation",
		Name:      "Reports Endpoint Deviation",
		DependsOn: []harnessx.CheckID{"baseline-probe"},
		Baseline:  harnessx.BaselineFromCheck("baseline-probe"),
		Capture: func(ctx context.Context, _ harnessx.Target, r harnessx.Resource, _ harnessx.ResultStore) (harnessx.Snapshot, error) {
			req, err := harnessx.NewRequestFromResource(ctx, r)
			if err != nil {
				return harnessx.Snapshot{}, err
			}
			return capture(ctx, req)
		},
	})

	// admin-bypass: manual baseline, custom comparator, attacker request
	// carrying the debug header.
	adminCheck := harnessx.NewBaselineCheck(harnessx.BaselineCheckConfig{
		ID:        "admin-bypass",
		Name:      "Admin Endpoint Authorization Bypass",
		DependsOn: []harnessx.CheckID{checkIDDiscovery},
		Baseline:  harnessx.BaselineFromResource(),
		Compare:   bypassComparator,
		Capture: func(ctx context.Context, _ harnessx.Target, r harnessx.Resource, _ harnessx.ResultStore) (harnessx.Snapshot, error) {
			req, err := harnessx.NewRequestFromResource(ctx, r, probe.WithHeader("X-Debug-Override", "true"))
			if err != nil {
				return harnessx.Snapshot{}, err
			}
			return capture(ctx, req)
		},
	})

	engine := harnessx.New(
		harnessx.WithChecks(discovery, baselineProbe, reportsCheck, adminCheck),
	)

	fmt.Printf("Scanning %s...\n\n", target.URL)
	summary, err := engine.Run(context.Background(), target)
	if err != nil {
		return fmt.Errorf("running scan: %w", err)
	}

	fmt.Printf("Checks: %d total, %d executed, %d skipped, %d failed\n",
		summary.TotalChecks, summary.Executed, summary.Skipped, summary.Failed)

	if len(summary.Observations) == 0 {
		fmt.Println("\nNo deviations found.")
		return nil
	}

	fmt.Println("\nDeviations:")
	for _, o := range summary.Observations {
		fmt.Printf("- [%s] %s (resource: %s)\n  %s\n", o.CheckID, o.Title, o.ResourceID, o.Description)
	}
	return nil
}
