package harnessx

// Scenario is a named, ordered subset of checks to execute against a target.
// Pass it to [Engine.RunScenario] to execute only those checks using the
// engine's configured reporters, concurrency limits, and default timeout.
//
// Checks can be shared across scenarios by referencing the same [CheckFunc]
// variable from multiple [Check] values, each with its own [Check.DependsOn]
// wiring — so the same business logic can run at different points in different
// scenario dependency graphs.
type Scenario struct {
	ID          string
	Name        string
	Description string
	Tags        []string
	Checks      []Check
}
