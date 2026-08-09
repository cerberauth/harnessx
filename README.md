# harnessx

[![GitHub Workflow Status](https://img.shields.io/github/actions/workflow/status/cerberauth/harnessx/ci.yml?branch=main&label=core%20build&style=for-the-badge)](https://github.com/cerberauth/harnessx/actions/workflows/ci.yml)
![Latest version](https://img.shields.io/github/v/release/cerberauth/harnessx?sort=semver&style=for-the-badge)
![Codecov](https://img.shields.io/codecov/c/gh/cerberauth/harnessx?token=BD1WPXJDAW&style=for-the-badge)
[![GoDoc reference](https://img.shields.io/badge/godoc-reference-5272B4.svg?style=for-the-badge)](https://godoc.org/github.com/cerberauth/harnessx)

**harnessx** is a concurrent, dependency-aware check orchestration engine for Go. It lets you define a graph of checks with explicit dependencies, run them in parallel waves, and collect structured observations — without writing any scheduling or concurrency boilerplate. Common use cases include security scanning, compliance checks, health checks, and quality gates.

---

## Features

| Feature | Description |
|---|---|
| DAG scheduling | Checks are topologically sorted and executed in parallel waves |
| Two execution scopes | `ScopeGlobal` runs once per target; `ScopePerResource` fans out over discovered resources |
| Resource discovery | Global checks can emit `Resource` objects consumed by downstream per-resource checks |
| Conditional execution | Skip checks based on prior results using composable `Condition` predicates |
| Skip decisions | Skip a whole check or individual resources at runtime via `SkipAlways` / `SkipWhen` / `SkipResourceWhen` |
| Bounded concurrency | Separate semaphores for level-wide and per-resource parallelism |
| Panic recovery | A panicking check is recorded as failed; the scan continues uninterrupted |
| Context cancellation | Full `context.Context` propagation with per-check timeouts |
| Reporter hooks | Real-time `OnCheckStart` / `OnCheckComplete` / `OnScanComplete` callbacks |
| Structured observations | Checks emit typed observations with title, description, evidence, and free-form metadata |
| Scenarios | Run named check subsets (REST scan, GraphQL scan) without executing all registered checks |
| Baseline comparison | Capture an expected response per resource and flag deviations, with pluggable comparison logic |
| Zero dependencies | Core engine (root package) is pure Go standard library; optional subpackages (`reporters`, `checkdef`) pull in their own deps only when imported |

---

## Installation

```bash
go get github.com/cerberauth/harnessx
```

Requires **Go 1.22+**.

---

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "net/http"

    "github.com/cerberauth/harnessx"
)

func main() {
    // 1. Describe the target.
    target := harnessx.Target{URL: "https://example.com", Host: "example.com"}

    // 2. Define checks.
    tlsCheck := harnessx.Check{
        ID:    "tls",
        Name:  "TLS configuration",
        Scope: harnessx.ScopeGlobal,
        Run: func(ctx context.Context, t harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
            resp, err := http.Get(t.URL)
            if err != nil || !resp.TLS.HandshakeComplete {
                return harnessx.Result{
                    Observations: []harnessx.Observation{{
                        Title: "TLS not negotiated",
                    }},
                }, nil
            }
            return harnessx.Result{}, nil
        },
    }

    headerCheck := harnessx.Check{
        ID:        "headers",
        Name:      "Security headers",
        Scope:     harnessx.ScopeGlobal,
        DependsOn: []harnessx.CheckID{"tls"},
        // Only runs if TLS passed cleanly.
        Conditions: []harnessx.Condition{harnessx.IfCheckPassed("tls")},
        Run: func(ctx context.Context, t harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
            resp, err := http.Get(t.URL)
            if err != nil {
                return harnessx.Result{}, err
            }
            var observations []harnessx.Observation
            if resp.Header.Get("Strict-Transport-Security") == "" {
                observations = append(observations, harnessx.Observation{
                    Title: "Missing HSTS header",
                })
            }
            return harnessx.Result{Observations: observations}, nil
        },
    }

    // 3. Create the engine and register checks.
    engine := harnessx.New(
        harnessx.WithMaxConcurrency(4),
        harnessx.WithChecks(tlsCheck, headerCheck),
    )

    // 4. Run the scan.
    summary, err := engine.Run(context.Background(), target)
    if err != nil {
        panic(err)
    }

    fmt.Printf("Executed: %d  Skipped: %d  Failed: %d\n",
        summary.Executed, summary.Skipped, summary.Failed)
    for _, o := range summary.Observations {
        fmt.Printf("%s: %s\n", o.Title, o.Description)
    }
}
```

---

## Concepts

### Checks

A `Check` is the fundamental unit of work. Each check has a unique `ID`, an execution `Scope`, and either a `Run` or `RunResource` function.

```go
type Check struct {
    ID          CheckID
    Name        string
    Description string
    Tags        []string

    // Dependency graph
    DependsOn  []CheckID
    Conditions []Condition // AND-evaluated; any false → skip

    // Skip control
    Skip SkipDecision // static/dynamic skip, optionally per-resource

    // Execution
    Scope       CheckScope     // ScopeGlobal or ScopePerResource
    Run         CheckFunc      // used when Scope == ScopeGlobal
    RunResource ResourceCheckFunc // used when Scope == ScopePerResource

    Timeout     time.Duration  // 0 → engine default (30s)
    Concurrency int            // per-resource parallelism; 0 → engine default
}
```

### Check Definitions

Hand-assembling `ID`, `Name`, `Description`, `Link`, `Tags`, and `DependsOn` in Go gets repetitive for a reusable check package. The `checkdef` subpackage parses that metadata from an embedded YAML, TOML, or JSON file instead:

```go
import "github.com/cerberauth/harnessx/checkdef"

type CheckDef = checkdef.CheckDef // ID, Name, Description, Link, Tags, DependsOn

func MustParseCheckDefYAML(pkg string, data []byte) CheckDef
func MustParseCheckDefTOML(pkg string, data []byte) CheckDef
func MustParseCheckDefJSON(pkg string, data []byte) CheckDef
```

Each `MustParse*` panics (prefixed with `pkg`) on malformed input — definitions are typically parsed once at `init()` time from an `//go:embed`ded file, so a bad definition is a build-time bug, not a runtime error to recover from.

```yaml
# check.yaml
id: alg_none
name: "Algorithm None"
description: "Tests if the server accepts tokens with the algorithm set to 'none'."
link: "https://example.com/vulnerabilities/jwt-alg-none"
tags:
  - algorithm
depends_on:
  - baseline
```

```go
//go:embed check.yaml
var checkYAML []byte

var def = checkdef.MustParseCheckDefYAML("algnone", checkYAML)

var Check = harnessx.Check{
    ID:          harnessx.CheckID(def.ID),
    Name:        def.Name,
    Description: def.Description,
    Link:        def.Link,
    Tags:        def.Tags,
    DependsOn:   def.DependsOnIDs(),
    Run:         run,
}
```

`checkdef` is a separate package from root `harnessx` so the core engine keeps its zero-dependency guarantee — the YAML/TOML parsers are only pulled in by code that imports `checkdef`.

### Scopes

| Scope | Runs | Function |
|---|---|---|
| `ScopeGlobal` | Once per scan | `Run(ctx, target, store) (Result, error)` |
| `ScopePerResource` | Once per resource discovered so far | `RunResource(ctx, target, resource, store) (Result, error)` |

### Resource Discovery

A `ScopeGlobal` check can return a `Resources` slice in its result. Those resources are accumulated in the engine's store and made available to all subsequent `ScopePerResource` checks.

```go
crawl := harnessx.Check{
    ID:    "crawl",
    Scope: harnessx.ScopeGlobal,
    Run: func(ctx context.Context, t harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
        endpoints := discover(ctx, t.URL) // returns []Resource
        return harnessx.Result{Resources: endpoints}, nil
    },
}

probe := harnessx.Check{
    ID:          "probe",
    Scope:       harnessx.ScopePerResource,
    DependsOn:   []harnessx.CheckID{"crawl"},
    RunResource: func(ctx context.Context, t harnessx.Target, r harnessx.Resource, _ harnessx.ResultStore) (harnessx.Result, error) {
        // called once for each Resource returned by "crawl"
        return test(ctx, r), nil
    },
}
```

### Conditions

Conditions gate whether a check runs. All conditions in a check's `Conditions` slice must pass (AND semantics). Built-in predicates:

| Predicate | Description |
|---|---|
| `IfCheckPassed(id)` | Prior check completed with no observations and no error |
| `IfCheckObserved(id)` | Prior check produced at least one observation |
| `IfCheckSkipped(id)` | Prior check was skipped |
| `All(c1, c2, ...)` | All conditions must hold |
| `Any(c1, c2, ...)` | At least one condition must hold |
| `Not(c)` | Negates a condition |

```go
// Run "deep-probe" only if "detect" produced any observations.
deepProbe := harnessx.Check{
    ID:         "deep-probe",
    Scope:      harnessx.ScopeGlobal,
    DependsOn:  []harnessx.CheckID{"detect"},
    Conditions: []harnessx.Condition{
        harnessx.IfCheckObserved("detect"),
    },
    Run: ...,
}
```

### Skip Decisions

`Skip` gates whether a check (or, for `ScopePerResource` checks, an individual resource) runs at all — evaluated before `Conditions` and before the check function. A non-empty reason skips and records `Result.SkipReason`; reporters still receive `OnCheckComplete` for it.

```go
type SkipDecision struct { /* built via SkipAlways / SkipWhen / SkipResourceWhen */ }

func SkipAlways(reason string) SkipDecision
func SkipWhen(fn func(ctx context.Context, target Target, store ResultStore) string) SkipDecision
func SkipResourceWhen(fn func(ctx context.Context, target Target, resource Resource, store ResultStore) string) SkipDecision
```

- `SkipAlways` / `SkipWhen` are check-wide: for a `ScopePerResource` check, a non-empty reason skips the **entire check** once, before it fans out over resources.
- `SkipResourceWhen` is evaluated **once per resource**, so different resources on the same check can be skipped for different reasons (or not at all).
- If a check's `Skip` has no per-resource decision, per-resource evaluation falls back to the check-wide one.

```go
// Skip the whole check if the target isn't HTTPS.
tlsOnly := harnessx.Check{
    ID:   "hsts-header",
    Skip: harnessx.SkipWhen(func(ctx context.Context, t harnessx.Target, _ harnessx.ResultStore) string {
        if !strings.HasPrefix(t.URL, "https://") {
            return "target is not HTTPS"
        }
        return ""
    }),
    Run: ...,
}

// Skip only resources that opted out via metadata.
endpointAuth := harnessx.Check{
    ID:    "endpoint-auth",
    Scope: harnessx.ScopePerResource,
    Skip: harnessx.SkipResourceWhen(func(ctx context.Context, t harnessx.Target, r harnessx.Resource, _ harnessx.ResultStore) string {
        if r.Metadata["auth"] == "none" {
            return "endpoint declares no auth"
        }
        return ""
    }),
    RunResource: ...,
}
```

### Execution Model

1. Checks are validated and sorted into parallel **levels** via Kahn's topological sort.
2. All checks within a level execute concurrently, bounded by `WithMaxConcurrency`.
3. Each level receives a **frozen snapshot** of the result store taken before any check in that level starts — intra-level races are impossible by design.
4. `ScopePerResource` checks within a level fan out over all currently known resources, bounded by `WithMaxResourceConcurrency` (or the check's own `Concurrency` field).

### Scenarios

A `Scenario` groups a named set of checks to be executed together. Use `RunScenario` instead of `Run` to execute only that subset — the engine's registered checks are ignored.

```go
restScenario := harnessx.Scenario{
    ID:   "rest-api",
    Name: "REST API Scan",
    Checks: []harnessx.Check{discoveryCheck, authCheck, schemaCheck},
}

summary, err := engine.RunScenario(ctx, target, restScenario)
```

To share business logic across scenarios while varying the dependency order, define the `Run` function as a variable and reference it in multiple `Check` values with different `DependsOn` fields:

```go
var checkAuthFn harnessx.CheckFunc = func(...) (harnessx.Result, error) { ... }

// REST: auth after endpoint discovery
restAuth := harnessx.Check{ID: "rest-auth", DependsOn: []harnessx.CheckID{"rest-discovery"}, Run: checkAuthFn}

// GraphQL: same logic, wired after schema introspection
gqlAuth  := harnessx.Check{ID: "gql-auth",  DependsOn: []harnessx.CheckID{"gql-introspection"}, Run: checkAuthFn}
```

### Reporter

Implement the `Reporter` interface to receive real-time events:

```go
type Reporter interface {
    OnScanStart(target Target, totalChecks int)
    OnCheckStart(check Check, target Target, resource *Resource)
    OnCheckComplete(result Result)
    OnScanComplete(summary ScanSummary)
}
```

`OnScanStart` fires before any check runs with the total registered check count — use it to initialise a progress bar. `OnScanComplete` is **always** called — even after a context cancellation or early error.

```go
engine := harnessx.New(
    harnessx.WithReporters(myReporter, otherReporter),
)
```

### Baseline Comparison

Baseline comparison detects the case where an endpoint's response *changes* in a way that indicates a bug — the canonical example being an endpoint that normally answers `401 Unauthorized` but, because of a broken authorization check, answers `200 OK` instead.

A `Baseline` is just a `Snapshot{StatusCode int, Header http.Header, Body []byte, Duration time.Duration, Data any}` — by default only the status code is compared, but `Header`/`Body`/`Duration` carry the full response, and `Data` can carry anything else, for custom comparators to inspect.

```go
type Snapshot struct {
    StatusCode int
    Header     http.Header
    Body       []byte
    Duration   time.Duration
    Data       any
}
type Baseline = Snapshot

type BaselineSource func(ctx context.Context, target Target, resource Resource, store ResultStore) (Baseline, bool)
type BaselineComparator func(baseline, current Snapshot) []Observation
```

A baseline is obtained one of two ways:

- **Baseline probe** — a dedicated check captures the expected response at scan time and stores it via `CaptureBaselineCheck`; downstream checks read it back with `BaselineFromCheck(id)`.
- **Manual** — a fixed value via `StaticBaseline(b)`, or a per-resource value attached to `Resource.Data` at discovery time and read back with `BaselineFromResource()`.

`NewBaselineCheck` wires a `BaselineSource`, a `Capture` function, and an optional `Compare` (defaults to `CompareStatusCode`) into a normal `Check`:

```go
// Baseline captured once, at runtime, per resource.
probeCheck := harnessx.CaptureBaselineCheck("baseline-probe", "Baseline Probe",
    func(ctx context.Context, t harnessx.Target, r harnessx.Resource, _ harnessx.ResultStore) (harnessx.Snapshot, error) {
        return captureUnauthenticated(ctx, r.URL) // your capture logic
    })

// Compared against a later, potentially malicious, attempt.
bypassCheck := harnessx.NewBaselineCheck(harnessx.BaselineCheckConfig{
    ID:        "auth-bypass",
    DependsOn: []harnessx.CheckID{"baseline-probe"},
    Baseline:  harnessx.BaselineFromCheck("baseline-probe"),
    Capture: func(ctx context.Context, t harnessx.Target, r harnessx.Resource, _ harnessx.ResultStore) (harnessx.Snapshot, error) {
        return captureWithForgedHeader(ctx, r.URL) // your capture logic
    },
    // Compare defaults to CompareStatusCode; override for custom semantics,
    // e.g. only flag a denied -> allowed transition:
    Compare: func(baseline, current harnessx.Snapshot) []harnessx.Observation {
        if baseline.StatusCode >= 400 && current.StatusCode < 300 {
            return []harnessx.Observation{{Title: "Authorization bypass"}}
        }
        return nil
    },
})
```

For targets with no per-resource dimension (a single token, a single endpoint), use the `ScopeGlobal` counterparts — `NewGlobalBaselineCheck`, `CaptureGlobalBaselineCheck`, `StaticGlobalBaseline`, `BaselineFromGlobalCheck` — same shape, minus the `Resource` param.

For checks that probe by swapping/omitting a credential (token, API key, session ID), `harnessx.ProbeAndCompareBaseline` takes a `probe.RequestBuilder` and skips the `Capture`/`Compare` wiring entirely:

```go
_, vulnerable, err := harnessx.ProbeAndCompareBaseline(ctx, p,
    func(ctx context.Context) (*http.Request, error) {
        return probe.NewRequest(ctx, http.MethodGet, resource.URL, nil, probe.WithBearerToken(forgedToken))
    }, store, "baseline-probe")
```

It builds the request via `probe.NewRequest` plus a named credential mutator (`WithBearerToken`, `WithBasicAuth`, `WithAPIKeyHeader`, `WithAPIKeyQuery`, `WithAuthCookie`, `WithFormCredential`), sends it via `probe.Do`, and diffs the resulting `Snapshot` against the baseline stored under `"baseline-probe"`.

See the [Baseline Comparison guide](/guides/baseline) and [`examples/baseline-scan`](./examples/baseline-scan/main.go) for a full runnable scenario.

---

## Examples

- [Advanced Scan](./examples/advanced-scan/main.go): A comprehensive example demonstrating multi-level dependencies, resource discovery, custom conditions, and a pretty-printing reporter.
- [Multi-Scenario Scan](./examples/multi-scenario/main.go): REST API and GraphQL API scenarios sharing business logic with different dependency graphs. Select a scenario at runtime via CLI argument.
- [Baseline Scan](./examples/baseline-scan/main.go): Detects an authorization bypass by comparing live HTTP responses against a per-resource baseline — one captured at runtime, one defined manually.

---

## API Reference

### Engine

```go
// New creates a new Engine with the given options.
func New(opts ...Option) *Engine

// Register adds checks to the engine. Returns ErrDuplicateCheckID if any ID conflicts.
func (e *Engine) Register(checks ...Check) error

// Run executes all registered checks against target.
// Always calls Reporter.OnScanComplete before returning.
func (e *Engine) Run(ctx context.Context, target Target) (ScanSummary, error)

// RunScenario executes only the checks in scenario against target.
// Ignores checks registered via Register or WithChecks.
// Always calls Reporter.OnScanComplete before returning.
func (e *Engine) RunScenario(ctx context.Context, target Target, scenario Scenario) (ScanSummary, error)
```

### Skip Decisions

```go
// SkipAlways always returns reason — the check (or resource) is always skipped.
func SkipAlways(reason string) SkipDecision

// SkipWhen evaluates fn once for the whole check.
func SkipWhen(fn func(ctx context.Context, target Target, store ResultStore) string) SkipDecision

// SkipResourceWhen evaluates fn once per resource, for ScopePerResource checks.
func SkipResourceWhen(fn func(ctx context.Context, target Target, resource Resource, store ResultStore) string) SkipDecision
```

### Baseline Comparison

```go
type Snapshot struct { StatusCode int; Header http.Header; Body []byte; Duration time.Duration; Data any }
type Baseline = Snapshot

// BaselineSource resolves the baseline for a resource; ok=false skips the check.
type BaselineSource func(ctx context.Context, target Target, resource Resource, store ResultStore) (Baseline, bool)

func StaticBaseline(b Baseline) BaselineSource
func BaselineFromResource() BaselineSource
func BaselineFromCheck(id CheckID) BaselineSource

// BaselineComparator judges the baseline against a freshly captured snapshot.
type BaselineComparator func(baseline, current Snapshot) []Observation

// CompareStatusCode is the default BaselineComparator.
func CompareStatusCode(baseline, current Snapshot) []Observation

type BaselineCheckConfig struct {
    ID, Name, Description string
    DependsOn             []CheckID
    Baseline              BaselineSource
    Capture               func(ctx context.Context, target Target, resource Resource, store ResultStore) (Snapshot, error)
    Compare               BaselineComparator // nil -> CompareStatusCode
    Timeout               time.Duration
    Concurrency           int
}

// NewBaselineCheck builds a ScopePerResource Check from cfg.
func NewBaselineCheck(cfg BaselineCheckConfig) Check

// CaptureBaselineCheck builds a ScopePerResource Check that captures a
// Snapshot per resource and stores it as Result.Data — the "baseline probe".
func CaptureBaselineCheck(id CheckID, name string, capture func(ctx context.Context, target Target, resource Resource, store ResultStore) (Snapshot, error)) Check

// ScopeGlobal counterparts, for targets with no per-resource dimension.
type GlobalBaselineSource func(ctx context.Context, target Target, store ResultStore) (Baseline, bool)
type GlobalCapture func(ctx context.Context, target Target, store ResultStore) (Snapshot, error)

func StaticGlobalBaseline(b Baseline) GlobalBaselineSource
func BaselineFromGlobalCheck(id CheckID) GlobalBaselineSource

type GlobalBaselineCheckConfig struct {
    ID, Name, Description string
    DependsOn              []CheckID
    Baseline               GlobalBaselineSource
    Capture                GlobalCapture
    Compare                BaselineComparator // nil -> CompareStatusCode
    Timeout                time.Duration
}

func NewGlobalBaselineCheck(cfg GlobalBaselineCheckConfig) Check
func CaptureGlobalBaselineCheck(id CheckID, name string, capture GlobalCapture) Check

// probe package: build a request from method+URL, then apply small
// composable mutators to it.
type RequestMutator func(*http.Request) error
type RequestBuilder func(ctx context.Context) (*http.Request, error)

func NewRequest(ctx context.Context, method, target string, body io.Reader, mutators ...RequestMutator) (*http.Request, error)
func WithHeader(name, value string) RequestMutator
func WithCookie(cookie *http.Cookie) RequestMutator
func WithQuery(name, value string) RequestMutator

// Named credential mutators — one per auth use case.
func WithBearerToken(token string) RequestMutator
func WithBasicAuth(username, password string) RequestMutator
func WithAPIKeyHeader(name, value string) RequestMutator
func WithAPIKeyQuery(name, value string) RequestMutator
func WithAuthCookie(name, value string) RequestMutator
func WithFormCredential(name, value string) RequestMutator

// NewRequestFromResource builds a request for r, defaulting to GET when
// r.Method is empty.
func NewRequestFromResource(ctx context.Context, r Resource, mutators ...probe.RequestMutator) (*http.Request, error)

// ProbeAndCompareBaseline sends the request returned by build via probe.Do,
// and compares the resulting Snapshot against the baseline stored under baselineID.
func ProbeAndCompareBaseline(ctx context.Context, p *probe.Probe, build probe.RequestBuilder, store ResultStore, baselineID CheckID) (Snapshot, bool, error)
```

### Options

| Option | Default | Description |
|---|---|---|
| `WithMaxConcurrency(n)` | `runtime.NumCPU()` | Maximum checks running concurrently within a level |
| `WithMaxResourceConcurrency(n)` | `runtime.NumCPU()` | Default maximum resource goroutines per check |
| `WithDefaultTimeout(d)` | `30s` | Per-check timeout when `Check.Timeout` is zero |
| `WithReporters(reporters...)` | `NoopReporter` | Real-time event callbacks (multiple reporters supported) |
| `WithChecks(checks...)` | — | Register checks at construction time |

### Errors

| Error | Meaning |
|---|---|
| `ErrNoChecks` | `Run` was called with no checks registered |
| `ErrDuplicateCheckID` | Two checks share the same `CheckID` |
| `ErrUnknownDependency` | A `DependsOn` entry references a non-existent check |
| `ErrCycleDetected` | The dependency graph contains a cycle |
| `*ScanError` | A check's `Run`/`RunResource` returned an error or panicked |

---

## License

This repository is licensed under the [MIT License](https://github.com/cerberauth/harnessx/blob/main/LICENSE) @ [CerberAuth](https://www.cerberauth.com/).

