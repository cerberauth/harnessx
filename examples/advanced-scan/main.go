package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cerberauth/harnessx"
)

type PrettyReporter struct{}

func (r *PrettyReporter) OnCheckStart(check harnessx.Check, target harnessx.Target, resource *harnessx.Resource) {
	fmt.Printf("🔍 Starting check: %-20s (Scope: %v)\n", check.ID, scopeToString(check.Scope))
}

const metadataKeyType = "type"

func (r *PrettyReporter) OnCheckComplete(result harnessx.Result) {
	status := "✅ PASSED"
	switch {
	case result.Err != nil:
		status = fmt.Sprintf("❌ FAILED: %v", result.Err)
	case result.Skipped:
		status = "⏭️  SKIPPED"
		if result.SkipReason != "" {
			status += " (" + result.SkipReason + ")"
		}
	case len(result.Observations) > 0:
		status = fmt.Sprintf("⚠️  FOUND %d issues", len(result.Observations))
	}

	resID := ""
	if result.ResourceID != "" {
		resID = fmt.Sprintf(" [%s]", result.ResourceID)
	}

	fmt.Printf("🏁 Check complete: %-20s%s -> %s\n", result.CheckID, resID, status)
}

func (r *PrettyReporter) OnScanComplete(summary harnessx.ScanSummary) {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("SCAN SUMMARY")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Target:   %s\n", summary.Target.URL)
	fmt.Printf("Duration: %v\n", summary.Duration)
	fmt.Printf("Checks:   %d total, %d executed, %d skipped, %d failed\n",
		summary.TotalChecks, summary.Executed, summary.Skipped, summary.Failed)

	if len(summary.Observations) > 0 {
		fmt.Println("\nObservations:")
		for _, o := range summary.Observations {
			fmt.Printf("- %s: %s\n", o.Title, o.Description)
			if o.ResourceID != "" {
				fmt.Printf("  Resource: %s\n", o.ResourceID)
			}
			if o.Evidence != "" {
				fmt.Printf("  Evidence: %s\n", o.Evidence)
			}
		}
	} else {
		fmt.Println("\nNo issues found. Everything looks good! ✨")
	}
}

func scopeToString(s harnessx.CheckScope) string {
	if s == harnessx.ScopeGlobal {
		return "Global"
	}
	return "PerResource"
}

func main() {
	target := harnessx.Target{
		URL:  "https://api.example.com",
		Host: "example.com",
		Metadata: map[string]string{
			"env": "production",
		},
	}

	discoveryCheck := harnessx.Check{
		ID:          "discovery",
		Name:        "Endpoint Discovery",
		Description: "Discovers API endpoints and static resources",
		Scope:       harnessx.ScopeGlobal,
		Run: func(ctx context.Context, t harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
			time.Sleep(100 * time.Millisecond)

			resources := []harnessx.Resource{
				{ID: "api-v1", URL: t.URL + "/v1", Metadata: map[string]string{metadataKeyType: "api"}},
				{ID: "api-v2", URL: t.URL + "/v2", Metadata: map[string]string{metadataKeyType: "api"}},
				{ID: "static", URL: "https://static.example.com", Metadata: map[string]string{metadataKeyType: "static"}},
			}

			return harnessx.Result{
				Resources: resources,
			}, nil
		},
	}

	statusCheck := harnessx.Check{
		ID:          "status",
		Name:        "Connectivity Check",
		Description: "Checks if the resource is reachable and returns 200 OK",
		Scope:       harnessx.ScopePerResource,
		DependsOn:   []harnessx.CheckID{"discovery"},
		RunResource: func(ctx context.Context, t harnessx.Target, r harnessx.Resource, _ harnessx.ResultStore) (harnessx.Result, error) {
			time.Sleep(50 * time.Millisecond)

			if r.ID == "api-v1" {
				return harnessx.Result{
					Observations: []harnessx.Observation{{
						Title:    "Endpoint Unreachable",
						Evidence: "Received HTTP 503 Service Unavailable",
					}},
				}, nil
			}

			return harnessx.Result{}, nil
		},
	}

	headersCheck := harnessx.Check{
		ID:          "headers",
		Name:        "Security Headers",
		Description: "Checks for recommended security headers",
		Scope:       harnessx.ScopePerResource,
		DependsOn:   []harnessx.CheckID{"status"},
		RunResource: func(ctx context.Context, t harnessx.Target, r harnessx.Resource, store harnessx.ResultStore) (harnessx.Result, error) {
			if status, ok := store.GetForResource("status", r.ID); ok && len(status.Observations) > 0 {
				return harnessx.Result{
					Skipped:    true,
					SkipReason: "Status check failed for this resource",
				}, nil
			}

			time.Sleep(30 * time.Millisecond)
			var observations []harnessx.Observation
			if r.ID == "api-v2" {
				observations = append(observations, harnessx.Observation{
					Title:       "Missing HSTS Header",
					Description: "Strict-Transport-Security header is missing",
				})
			}

			return harnessx.Result{Observations: observations}, nil
		},
	}

	probeCheck := harnessx.Check{
		ID:          "deep-probe",
		Name:        "Deep Probe",
		Description: "Performs intensive checks when any issues are found",
		Scope:       harnessx.ScopePerResource,
		DependsOn:   []harnessx.CheckID{"headers"},
		Conditions: []harnessx.Condition{
			func(store harnessx.ResultStore) bool {
				return len(store.Observations()) > 0
			},
		},
		RunResource: func(ctx context.Context, t harnessx.Target, r harnessx.Resource, store harnessx.ResultStore) (harnessx.Result, error) {
			if h, ok := store.GetForResource("headers", r.ID); ok && len(h.Observations) > 0 {
				return harnessx.Result{}, nil
			}

			time.Sleep(200 * time.Millisecond)
			return harnessx.Result{
				Observations: []harnessx.Observation{{
					Title:    "Insecure Parameter Handling",
					Evidence: "Found insecure parameter handling in " + r.URL,
				}},
			}, nil
		},
	}

	reportCheck := harnessx.Check{
		ID:        "final-report",
		Name:      "Emergency Report",
		Scope:     harnessx.ScopeGlobal,
		DependsOn: []harnessx.CheckID{"deep-probe"},
		Conditions: []harnessx.Condition{
			func(store harnessx.ResultStore) bool {
				return len(store.Observations()) > 0
			},
		},
		Run: func(ctx context.Context, t harnessx.Target, store harnessx.ResultStore) (harnessx.Result, error) {
			fmt.Println("\n🚨 CRITICAL ISSUES DETECTED - NOTIFYING ONCALL 🚨")
			return harnessx.Result{}, nil
		},
	}

	engine := harnessx.New(
		harnessx.WithMaxConcurrency(4),
		harnessx.WithDefaultTimeout(2*time.Second),
		harnessx.WithReporters(&PrettyReporter{}),
		harnessx.WithChecks(discoveryCheck, statusCheck, headersCheck, probeCheck, reportCheck),
	)

	fmt.Printf("🚀 Starting scan for %s...\n\n", target.URL)
	_, err := engine.Run(context.Background(), target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running scan: %v\n", err)
		os.Exit(1)
	}
}
