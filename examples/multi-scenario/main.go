package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cerberauth/harnessx"
)

// checkAuthFn is a shared CheckFunc referenced by both REST and GraphQL auth
// checks. Each scenario wires it at a different point in its dependency graph.
var checkAuthFn harnessx.CheckFunc = func(ctx context.Context, t harnessx.Target, store harnessx.ResultStore) (harnessx.Result, error) {
	time.Sleep(40 * time.Millisecond)
	fmt.Printf("   auth: verifying credentials against %s\n", t.Host)
	return harnessx.Result{}, nil
}

// ── REST API scenario ──────────────────────────────────────────────────────────

var restDiscovery = harnessx.Check{
	ID:          "rest-discovery",
	Name:        "REST Endpoint Discovery",
	Description: "Crawls the API to enumerate REST endpoints",
	Scope:       harnessx.ScopeGlobal,
	Run: func(ctx context.Context, t harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
		time.Sleep(60 * time.Millisecond)
		return harnessx.Result{
			Resources: []harnessx.Resource{
				{ID: "users", URL: t.URL + "/users", Method: "GET"},
				{ID: "posts", URL: t.URL + "/posts", Method: "GET"},
			},
		}, nil
	},
}

var restAuth = harnessx.Check{
	ID:          "rest-auth",
	Name:        "REST Authentication",
	Description: "Verifies authentication is enforced on REST endpoints",
	Scope:       harnessx.ScopeGlobal,
	DependsOn:   []harnessx.CheckID{"rest-discovery"},
	Run:         checkAuthFn,
}

var restSchema = harnessx.Check{
	ID:          "rest-schema",
	Name:        "REST Schema Validation",
	Description: "Validates each endpoint response matches its OpenAPI schema",
	Scope:       harnessx.ScopePerResource,
	DependsOn:   []harnessx.CheckID{"rest-auth"},
	RunResource: func(ctx context.Context, t harnessx.Target, r harnessx.Resource, _ harnessx.ResultStore) (harnessx.Result, error) {
		time.Sleep(30 * time.Millisecond)
		fmt.Printf("   schema: validating %s %s\n", r.Method, r.URL)
		return harnessx.Result{}, nil
	},
}

var restScenario = harnessx.Scenario{
	ID:          "rest-api",
	Name:        "REST API Scan",
	Description: "Discovers endpoints, verifies auth, then validates each endpoint schema",
	Tags:        []string{"rest", "openapi"},
	Checks:      []harnessx.Check{restDiscovery, restAuth, restSchema},
}

// ── GraphQL API scenario ───────────────────────────────────────────────────────
// Auth runs after schema introspection — different ordering than REST but
// the same checkAuthFn business logic.

var gqlIntrospection = harnessx.Check{
	ID:          "gql-introspection",
	Name:        "GraphQL Schema Introspection",
	Description: "Fetches the full schema via the introspection query",
	Scope:       harnessx.ScopeGlobal,
	Run: func(ctx context.Context, t harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
		time.Sleep(50 * time.Millisecond)
		fmt.Printf("   introspection: fetched schema from %s/graphql\n", t.URL)
		return harnessx.Result{}, nil
	},
}

var gqlAuth = harnessx.Check{
	ID:          "gql-auth",
	Name:        "GraphQL Authentication",
	Description: "Verifies authentication is enforced on the GraphQL endpoint",
	Scope:       harnessx.ScopeGlobal,
	DependsOn:   []harnessx.CheckID{"gql-introspection"},
	Run:         checkAuthFn, // same function, different position in the graph
}

var gqlDepthLimit = harnessx.Check{
	ID:          "gql-depth-limit",
	Name:        "Query Depth Limit",
	Description: "Sends deeply nested queries to verify depth limiting is enforced",
	Scope:       harnessx.ScopeGlobal,
	DependsOn:   []harnessx.CheckID{"gql-auth"},
	Run: func(ctx context.Context, t harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
		time.Sleep(35 * time.Millisecond)
		fmt.Printf("   depth-limit: probing %s/graphql\n", t.URL)
		return harnessx.Result{}, nil
	},
}

var gqlScenario = harnessx.Scenario{
	ID:          "graphql-api",
	Name:        "GraphQL API Scan",
	Description: "Introspects schema, verifies auth, then checks depth and complexity limits",
	Tags:        []string{"graphql"},
	Checks:      []harnessx.Check{gqlIntrospection, gqlAuth, gqlDepthLimit},
}

// ── Shared reporter ────────────────────────────────────────────────────────────

type PrettyReporter struct{ scenario harnessx.Scenario }

func (r *PrettyReporter) OnScanStart(_ harnessx.Target, n int) {
	fmt.Printf("Running scenario %q — %d checks\n\n", r.scenario.Name, n)
}

func (r *PrettyReporter) OnCheckStart(check harnessx.Check, _ harnessx.Target, resource *harnessx.Resource) {
	if resource != nil {
		fmt.Printf(" ▸ %s [%s]\n", check.Name, resource.ID)
	} else {
		fmt.Printf(" ▸ %s\n", check.Name)
	}
}

func (r *PrettyReporter) OnCheckComplete(result harnessx.Result) {
	switch {
	case result.Err != nil:
		fmt.Printf("   ❌ failed: %v\n", result.Err)
	case result.Skipped:
		fmt.Printf("   ⏭  skipped\n")
	case len(result.Observations) > 0:
		fmt.Printf("   ⚠️  %d observation(s)\n", len(result.Observations))
	default:
		fmt.Printf("   ✅ ok\n")
	}
}

func (r *PrettyReporter) OnScanComplete(summary harnessx.ScanSummary) {
	fmt.Println("\n" + strings.Repeat("─", 50))
	fmt.Printf("%s — %d executed, %d skipped, %d failed, %v\n",
		r.scenario.Name, summary.Executed, summary.Skipped, summary.Failed, summary.Duration.Round(time.Millisecond))
	if len(summary.Observations) == 0 {
		fmt.Println("No observations. ✨")
	}
}

// ── main ───────────────────────────────────────────────────────────────────────

// Available scenarios indexed by ID for easy lookup.
var scenarios = map[string]harnessx.Scenario{
	restScenario.ID: restScenario,
	gqlScenario.ID:  gqlScenario,
}

func main() {
	target := harnessx.Target{
		URL:  "https://api.example.com",
		Host: "example.com",
	}

	scenarioID := "rest-api"
	if len(os.Args) > 1 {
		scenarioID = os.Args[1]
	}

	scenario, ok := scenarios[scenarioID]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown scenario %q (available: rest-api, graphql-api)\n", scenarioID)
		os.Exit(1)
	}

	engine := harnessx.New(
		harnessx.WithMaxConcurrency(4),
		harnessx.WithDefaultTimeout(5*time.Second),
		harnessx.WithReporters(&PrettyReporter{scenario: scenario}),
	)

	_, err := engine.RunScenario(context.Background(), target, scenario)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan error: %v\n", err)
		os.Exit(1)
	}
}
