package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cerberauth/harnessx"
)

// PrettyReporter is a custom reporter that prints scan progress to the console
// with icons and colors (using ANSI escape codes).
type PrettyReporter struct{}

func (r *PrettyReporter) OnCheckStart(check harnessx.Check, target harnessx.Target) {
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
	case len(result.Findings) > 0:
		status = fmt.Sprintf("⚠️  FOUND %d issues", len(result.Findings))
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

	if len(summary.Findings) > 0 {
		fmt.Println("\nFindings:")
		for _, f := range summary.Findings {
			severityColor := getSeverityColor(f.Severity)
			fmt.Printf("- [%s%s\033[0m] %s: %s\n", severityColor, f.Severity, f.Title, f.Description)
			if f.ResourceID != "" {
				fmt.Printf("  Resource: %s\n", f.ResourceID)
			}
		}
	} else {
		fmt.Println("\nNo findings found. Everything looks good! ✨")
	}
}

func scopeToString(s harnessx.CheckScope) string {
	if s == harnessx.ScopeGlobal {
		return "Global"
	}
	return "PerResource"
}

func getSeverityColor(s harnessx.Severity) string {
	switch s {
	case harnessx.SeverityCritical:
		return "\033[1;31m" // Bold Red
	case harnessx.SeverityHigh:
		return "\033[0;31m" // Red
	case harnessx.SeverityMedium:
		return "\033[0;33m" // Yellow
	case harnessx.SeverityLow:
		return "\033[0;34m" // Blue
	default:
		return "\033[0;37m" // Gray
	}
}

func main() {
	// 1. Define the target
	target := harnessx.Target{
		URL:  "https://api.example.com",
		Host: "example.com",
		Metadata: map[string]string{
			"env": "production",
		},
	}

	// 2. Define the discovery check
	// This check simulates discovering multiple endpoints on the target.
	discoveryCheck := harnessx.Check{
		ID:          "discovery",
		Name:        "Endpoint Discovery",
		Description: "Discovers API endpoints and static resources",
		Scope:       harnessx.ScopeGlobal,
		Run: func(ctx context.Context, t harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
			// Simulate network delay
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

	// 3. Define a per-resource status check
	// This check will run for EACH resource discovered by the discovery check.
	statusCheck := harnessx.Check{
		ID:          "status",
		Name:        "Connectivity Check",
		Description: "Checks if the resource is reachable and returns 200 OK",
		Scope:       harnessx.ScopePerResource,
		DependsOn:   []harnessx.CheckID{"discovery"},
		RunResource: func(ctx context.Context, t harnessx.Target, r harnessx.Resource, _ harnessx.ResultStore) (harnessx.Result, error) {
			time.Sleep(50 * time.Millisecond)

			// Simulate a failure for api-v1
			if r.ID == "api-v1" {
				return harnessx.Result{
					Findings: []harnessx.Finding{{
						Title:    "Endpoint Unreachable",
						Severity: harnessx.SeverityHigh,
						Evidence: "Received HTTP 503 Service Unavailable",
					}},
				}, nil
			}

			return harnessx.Result{}, nil
		},
	}

	// 4. Define a security headers check
	// We check for the status check result manually inside RunResource for per-resource gating.
	headersCheck := harnessx.Check{
		ID:          "headers",
		Name:        "Security Headers",
		Description: "Checks for recommended security headers",
		Scope:       harnessx.ScopePerResource,
		DependsOn:   []harnessx.CheckID{"status"},
		RunResource: func(ctx context.Context, t harnessx.Target, r harnessx.Resource, store harnessx.ResultStore) (harnessx.Result, error) {
			// Manual per-resource gating: only run if status check for THIS resource passed
			if status, ok := store.GetForResource("status", r.ID); ok && len(status.Findings) > 0 {
				return harnessx.Result{
					Skipped:    true,
					SkipReason: "Status check failed for this resource",
				}, nil
			}

			time.Sleep(30 * time.Millisecond)
			findings := []harnessx.Finding{}
			if r.ID == "api-v2" {
				findings = append(findings, harnessx.Finding{
					Title:       "Missing HSTS Header",
					Description: "Strict-Transport-Security header is missing",
					Severity:    harnessx.SeverityMedium,
				})
			}

			return harnessx.Result{Findings: findings}, nil
		},
	}

	// 5. Define an advanced vulnerability check
	// This one uses a custom condition to decide whether to run at all based on ANY medium findings so far.
	vulnerabilityCheck := harnessx.Check{
		ID:          "vuln-scan",
		Name:        "Deep Vulnerability Scan",
		Description: "Performs intensive scanning when any medium issues are found",
		Scope:       harnessx.ScopePerResource,
		DependsOn:   []harnessx.CheckID{"headers"},
		Conditions: []harnessx.Condition{
			func(store harnessx.ResultStore) bool {
				// Custom condition: run only if we found at least one Medium finding in any check
				return len(store.FindingsBySeverity(harnessx.SeverityMedium)) > 0
			},
		},
		RunResource: func(ctx context.Context, t harnessx.Target, r harnessx.Resource, store harnessx.ResultStore) (harnessx.Result, error) {
			// Again, manual gating: don't run on resources that already have problems
			if h, ok := store.GetForResource("headers", r.ID); ok && len(h.Findings) > 0 {
				return harnessx.Result{}, nil
			}

			time.Sleep(200 * time.Millisecond)
			return harnessx.Result{
				Findings: []harnessx.Finding{{
					Title:    "Potential IDOR",
					Severity: harnessx.SeverityCritical,
					Evidence: "Found insecure parameter handling in " + r.URL,
				}},
			}, nil
		},
	}

	// 6. Define a final report check
	// Only runs at the end if there were any critical findings.
	reportCheck := harnessx.Check{
		ID:        "final-report",
		Name:      "Emergency Report",
		Scope:     harnessx.ScopeGlobal,
		DependsOn: []harnessx.CheckID{"vuln-scan"},
		Conditions: []harnessx.Condition{
			func(store harnessx.ResultStore) bool {
				return len(store.FindingsBySeverity(harnessx.SeverityCritical)) > 0
			},
		},
		Run: func(ctx context.Context, t harnessx.Target, store harnessx.ResultStore) (harnessx.Result, error) {
			fmt.Println("\n🚨 CRITICAL VULNERABILITIES DETECTED - NOTIFYING ONCALL 🚨")
			return harnessx.Result{}, nil
		},
	}

	// 7. Initialize the engine with options
	engine := harnessx.New(
		harnessx.WithMaxConcurrency(4),
		harnessx.WithDefaultTimeout(2*time.Second),
		harnessx.WithReporters(&PrettyReporter{}),
		harnessx.WithChecks(discoveryCheck, statusCheck, headersCheck, vulnerabilityCheck, reportCheck),
	)

	// 7. Run the scan
	fmt.Printf("🚀 Starting Advanced Security Scan for %s...\n\n", target.URL)
	_, err := engine.Run(context.Background(), target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running scan: %v\n", err)
		os.Exit(1)
	}
}
