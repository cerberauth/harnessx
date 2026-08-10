package harnessx

import (
	"context"
	"fmt"
	"sync"
	"time"
)

func runVariants(ctx context.Context, variants []string, mode VariantMode, fn func(context.Context, string) (Result, error)) Result {
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
