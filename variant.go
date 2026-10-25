package harnessx

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RunVariants calls fn once per entry in variants and merges the outcomes into
// a single Result, the same way the engine does for a check with Variants. It
// lets code that wraps variant checks outside the engine reuse that fan-out
// instead of reimplementing it.
//
// mode selects the schedule: VariantsSequential (the zero value) runs them one
// at a time in list order, VariantsParallel runs them concurrently. Either way
// every variant runs; one failing does not stop the others.
//
// Aggregation:
//   - Result.Attempts holds one Attempt per variant, in the order of variants,
//     with its Variant, Observations, Resources, Data, Duration and Err.
//   - Result.Observations and Result.Resources are the attempts' slices
//     concatenated in that same order. An Observation with an empty Variant
//     gets the name of the variant that produced it.
//   - Result.Err is the first non-nil attempt error in variants order, not
//     completion order, so it is deterministic in parallel mode too. An
//     attempt's error is fn's returned error, or else the Err on its Result.
//   - A panic in fn is recovered and recorded as that attempt's Err, a
//     *ScanError, leaving the other variants unaffected.
//
// Result.Data is not set: each attempt's Data stays on its own Attempt.
func RunVariants(ctx context.Context, variants []string, mode VariantMode, fn func(context.Context, string) (Result, error)) Result {
	attempts := make([]Attempt, len(variants))

	if mode == VariantsParallel {
		var wg sync.WaitGroup
		for i, variant := range variants {
			wg.Add(1)
			go func(i int, variant string) {
				defer wg.Done()
				attempts[i] = runVariantAttempt(ctx, variant, fn)
			}(i, variant)
		}
		wg.Wait()
	} else {
		for i, variant := range variants {
			attempts[i] = runVariantAttempt(ctx, variant, fn)
		}
	}

	return aggregateAttempts(attempts)
}

func runVariantAttempt(ctx context.Context, variant string, fn func(context.Context, string) (Result, error)) (attempt Attempt) {
	attempt.Variant = variant

	start := time.Now()
	defer func() {
		attempt.Duration = time.Since(start)
		if r := recover(); r != nil {
			attempt.Err = &ScanError{Cause: fmt.Errorf("panic: %v", r)}
		}
	}()

	result, err := fn(ctx, variant)
	for i := range result.Observations {
		if result.Observations[i].Variant == "" {
			result.Observations[i].Variant = variant
		}
	}
	attempt.Observations = result.Observations
	attempt.Resources = result.Resources
	attempt.Data = result.Data
	if err != nil {
		attempt.Err = err
	} else {
		attempt.Err = result.Err
	}
	return attempt
}

func aggregateAttempts(attempts []Attempt) Result {
	result := Result{Attempts: attempts}
	for _, a := range attempts {
		result.Observations = append(result.Observations, a.Observations...)
		result.Resources = append(result.Resources, a.Resources...)
		if result.Err == nil && a.Err != nil {
			result.Err = a.Err
		}
	}
	return result
}
