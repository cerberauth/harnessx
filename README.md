<h1 align="center">harnessx</h1>

<p align="center">
  <a href="https://github.com/cerberauth/harnessx/actions/workflows/ci.yml"><img src="https://github.com/cerberauth/harnessx/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/cerberauth/harnessx"><img src="https://pkg.go.dev/badge/github.com/cerberauth/harnessx.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/cerberauth/harnessx"><img src="https://goreportcard.com/badge/github.com/cerberauth/harnessx" alt="Go Report Card"></a>
  <a href="https://github.com/cerberauth/harnessx/blob/main/LICENSE"><img src="https://img.shields.io/github/license/cerberauth/harnessx" alt="License"></a>
</p>

<p align="center">
  <a href="#features">Features</a> •
  <a href="#installation">Installation</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#concepts">Concepts</a> •
  <a href="#api-reference">API Reference</a>
</p>

**harnessx** is a concurrent, dependency-aware security check orchestration engine for Go. It lets you define a graph of security checks with explicit dependencies, run them in parallel waves, and collect structured findings — without writing any scheduling or concurrency boilerplate.

---

## Features

| Feature | Description |
|---|---|
| DAG scheduling | Checks are topologically sorted and executed in parallel waves |
| Two execution scopes | `ScopeGlobal` runs once per target; `ScopePerResource` fans out over discovered resources |
| Resource discovery | Global checks can emit `Resource` objects consumed by downstream per-resource checks |
| Conditional execution | Skip checks based on prior results using composable `Condition` predicates |
| Bounded concurrency | Separate semaphores for level-wide and per-resource parallelism |
| Panic recovery | A panicking check is recorded as failed; the scan continues uninterrupted |
| Context cancellation | Full `context.Context` propagation with per-check timeouts |
| Reporter hooks | Real-time `OnCheckStart` / `OnCheckComplete` / `OnScanComplete` callbacks |
| Structured findings | Findings carry severity (`info` → `critical`), evidence, and free-form metadata |
| Zero dependencies | Pure Go standard library — no external runtime dependencies |

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
                    Findings: []harnessx.Finding{{
                        Title:    "TLS not negotiated",
                        Severity: harnessx.SeverityHigh,
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
            var findings []harnessx.Finding
            if resp.Header.Get("Strict-Transport-Security") == "" {
                findings = append(findings, harnessx.Finding{
                    Title:    "Missing HSTS header",
                    Severity: harnessx.SeverityMedium,
                })
            }
            return harnessx.Result{Findings: findings}, nil
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
    for _, f := range summary.Findings {
        fmt.Printf("[%s] %s\n", f.Severity, f.Title)
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

    // Execution
    Scope       CheckScope     // ScopeGlobal or ScopePerResource
    Run         CheckFunc      // used when Scope == ScopeGlobal
    RunResource ResourceCheckFunc // used when Scope == ScopePerResource

    Timeout     time.Duration  // 0 → engine default (30s)
    Concurrency int            // per-resource parallelism; 0 → engine default
}
```

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
| `IfCheckPassed(id)` | Prior check completed with no findings and no error |
| `IfCheckFound(id, minSeverity)` | Prior check produced at least one finding at or above the given severity |
| `IfCheckSkipped(id)` | Prior check was skipped |
| `All(c1, c2, ...)` | All conditions must hold |
| `Any(c1, c2, ...)` | At least one condition must hold |
| `Not(c)` | Negates a condition |

```go
// Run "exploit" only if "detect" found a high or critical issue.
exploit := harnessx.Check{
    ID:         "exploit",
    Scope:      harnessx.ScopeGlobal,
    DependsOn:  []harnessx.CheckID{"detect"},
    Conditions: []harnessx.Condition{
        harnessx.IfCheckFound("detect", harnessx.SeverityHigh),
    },
    Run: ...,
}
```

### Execution Model

1. Checks are validated and sorted into parallel **levels** via Kahn's topological sort.
2. All checks within a level execute concurrently, bounded by `WithMaxConcurrency`.
3. Each level receives a **frozen snapshot** of the result store taken before any check in that level starts — intra-level races are impossible by design.
4. `ScopePerResource` checks within a level fan out over all currently known resources, bounded by `WithMaxResourceConcurrency` (or the check's own `Concurrency` field).

### Reporter

Implement the `Reporter` interface to receive real-time events:

```go
type Reporter interface {
    OnCheckStart(check Check, target Target)
    OnCheckComplete(result Result)
    OnScanComplete(summary ScanSummary)
}
```

`OnScanComplete` is **always** called — even after a context cancellation or early error.

```go
engine := harnessx.New(
    harnessx.WithReporter(myReporter),
)
```

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
```

### Options

| Option | Default | Description |
|---|---|---|
| `WithMaxConcurrency(n)` | `runtime.NumCPU()` | Maximum checks running concurrently within a level |
| `WithMaxResourceConcurrency(n)` | `runtime.NumCPU()` | Default maximum resource goroutines per check |
| `WithDefaultTimeout(d)` | `30s` | Per-check timeout when `Check.Timeout` is zero |
| `WithReporter(r)` | `NoopReporter` | Real-time event callbacks |
| `WithChecks(checks...)` | — | Register checks at construction time |

### Severities

`SeverityInfo` < `SeverityLow` < `SeverityMedium` < `SeverityHigh` < `SeverityCritical`

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

harnessx is licensed under the [MIT License](LICENSE).
