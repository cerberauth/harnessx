package harnessx

import (
	"cmp"
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/cerberauth/harnessx/internal/semaphore"
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
//   - Result.Skipped is set only when every variant was skipped, with the
//     SkipReason of the first one.
//   - A panic in fn is recovered and recorded as that attempt's Err, a
//     *ScanError, leaving the other variants unaffected.
//
// If ctx is cancelled, variants that have not started do not run: each is
// recorded as an Attempt whose Err is ctx.Err().
//
// Result.Data is not set: each attempt's Data stays on its own Attempt.
func RunVariants(ctx context.Context, variants []string, mode VariantMode, fn func(context.Context, string) (Result, error)) Result {
	return runVariants(ctx, variants, mode, variantLimits{}, fn)
}

// variantLimits bounds how a check's variants run; the zero value is no limit.
type variantLimits struct {
	// concurrency caps the variants in flight at once in VariantsParallel mode.
	concurrency int
	// timeout bounds each variant on its own, on top of the check's timeout.
	timeout time.Duration
}

func runVariants(ctx context.Context, variants []string, mode VariantMode, limits variantLimits, fn func(context.Context, string) (Result, error)) Result {
	attempts := make([]Attempt, len(variants))

	if mode == VariantsParallel {
		var sem *semaphore.Semaphore
		if limits.concurrency > 0 {
			sem = semaphore.New(limits.concurrency)
		}
		var wg sync.WaitGroup
		for i, variant := range variants {
			wg.Add(1)
			go func(i int, variant string) {
				defer wg.Done()
				if sem != nil {
					if !sem.Acquire(ctx.Done()) {
						attempts[i] = Attempt{Variant: variant, Err: ctx.Err()}
						return
					}
					defer sem.Release()
				}
				attempts[i] = runVariantAttempt(ctx, variant, limits.timeout, fn)
			}(i, variant)
		}
		wg.Wait()
	} else {
		for i, variant := range variants {
			if err := ctx.Err(); err != nil {
				attempts[i] = Attempt{Variant: variant, Err: err}
				continue
			}
			attempts[i] = runVariantAttempt(ctx, variant, limits.timeout, fn)
		}
	}

	return aggregateAttempts(attempts)
}

func runVariantAttempt(ctx context.Context, variant string, timeout time.Duration, fn func(context.Context, string) (Result, error)) (attempt Attempt) {
	attempt.Variant = variant

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	start := time.Now()
	defer func() {
		attempt.Duration = time.Since(start)
		if r := recover(); r != nil {
			attempt.Err = &ScanError{Cause: fmt.Errorf("panic: %v", r), Stack: debug.Stack()}
		}
	}()

	result, err := fn(ctx, variant)
	attempt.Observations = fillObservations(result.Observations, "", "", variant)
	attempt.Resources = result.Resources
	attempt.Skipped, attempt.SkipReason = result.Skipped, result.SkipReason
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
	skipped := 0
	for _, a := range attempts {
		result.Observations = append(result.Observations, a.Observations...)
		result.Resources = append(result.Resources, a.Resources...)
		if result.Err == nil && a.Err != nil {
			result.Err = a.Err
		}
		if a.Skipped {
			skipped++
		}
	}
	if len(attempts) > 0 && skipped == len(attempts) {
		result.Skipped = true
		result.SkipReason = cmp.Or(attempts[0].SkipReason, "all variants skipped")
	}
	return result
}
