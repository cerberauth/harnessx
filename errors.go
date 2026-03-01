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
)

type ScanError struct {
	CheckID CheckID
	Cause   error
}

func (e *ScanError) Error() string {
	return fmt.Sprintf("harnessx: check %q failed: %v", e.CheckID, e.Cause)
}

func (e *ScanError) Unwrap() error { return e.Cause }
