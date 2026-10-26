package harnessx

import (
	"errors"
	"fmt"
)

var (
	ErrCycleDetected     = errors.New("harnessx: dependency cycle detected")
	ErrDuplicateCheckID  = errors.New("harnessx: duplicate check ID")
	ErrUnknownDependency = errors.New("harnessx: unknown dependency check ID")
	ErrNoChecks          = errors.New("harnessx: no checks registered")

	// ErrUnknownCheckID is returned by Run and RunScenario when WithOnly names a
	// check ID that does not exist.
	ErrUnknownCheckID = errors.New("harnessx: unknown check ID in WithOnly")

	// ErrNoBaseline is returned by ProbeAndCompareBaseline when nothing is
	// stored under the baseline check's ID to compare against.
	ErrNoBaseline = errors.New("harnessx: no baseline available")
)

// ScanError records a failed check — typically a recovered panic — and is
// stored in Result.Err. CheckID identifies the check when known; Stack holds the
// goroutine stack at the point of a recovered panic.
type ScanError struct {
	CheckID CheckID
	Cause   error
	Stack   []byte
}

func (e *ScanError) Error() string {
	if e.CheckID == "" {
		return fmt.Sprintf("harnessx: check failed: %v", e.Cause)
	}
	return fmt.Sprintf("harnessx: check %q failed: %v", e.CheckID, e.Cause)
}

func (e *ScanError) Unwrap() error { return e.Cause }
