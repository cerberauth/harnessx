package harnessx

import (
	"fmt"
	"slices"
	"strings"
)

// levels groups checks into parallel execution levels (Kahn's algorithm): every
// check in a level depends only on checks in earlier levels. It returns
// ErrUnknownDependency (naming the check and the missing dependency) if a check
// depends on an ID that is not in checks, and ErrCycleDetected (listing the
// checks that could not be ordered) if the dependencies form a cycle.
func levels(checks []Check) ([][]Check, error) {
	byID := make(map[CheckID]Check, len(checks))
	for _, c := range checks {
		byID[c.ID] = c
	}

	inDegree := make(map[CheckID]int, len(checks))
	dependents := make(map[CheckID][]CheckID, len(checks))
	for _, c := range checks {
		inDegree[c.ID] += 0
		for _, dep := range c.DependsOn {
			if _, ok := byID[dep]; !ok {
				return nil, fmt.Errorf("%w: check %q depends on %q", ErrUnknownDependency, c.ID, dep)
			}
			dependents[dep] = append(dependents[dep], c.ID)
			inDegree[c.ID]++
		}
	}

	var queue []CheckID
	for _, c := range checks {
		if inDegree[c.ID] == 0 {
			queue = append(queue, c.ID)
		}
	}

	var out [][]Check
	visited := 0
	for len(queue) > 0 {
		level := make([]Check, 0, len(queue))
		for _, id := range queue {
			level = append(level, byID[id])
		}
		out = append(out, level)
		visited += len(queue)

		var next []CheckID
		for _, id := range queue {
			for _, dependent := range dependents[id] {
				inDegree[dependent]--
				if inDegree[dependent] == 0 {
					next = append(next, dependent)
				}
			}
		}
		queue = next
	}

	if visited != len(checks) {
		var stuck []string
		for _, c := range checks {
			if inDegree[c.ID] > 0 {
				stuck = append(stuck, string(c.ID))
			}
		}
		slices.Sort(stuck)
		return nil, fmt.Errorf("%w: cannot order %s", ErrCycleDetected, strings.Join(stuck, ", "))
	}
	return out, nil
}
