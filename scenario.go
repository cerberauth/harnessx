package harnessx

// Scenario is a named, ordered subset of checks to execute against a target.
// Pass it to [Engine.RunScenario] to execute only those checks using the
// engine's concurrency limits and default timeout.
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

	// Reporters, when non-empty, receive callbacks for this RunScenario call
	// instead of the engine's configured reporters (see [WithReporters]).
	// Use this when an [Engine] is shared across independent callers that
	// each need their own reporters.
	Reporters []Reporter
}
