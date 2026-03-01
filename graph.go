package harnessx

type graph struct {
	checks   []Check
	inDegree map[CheckID]int
	adjList  map[CheckID][]CheckID
	byID     map[CheckID]Check
}

func newGraph(checks []Check) (*graph, error) {
	byID := make(map[CheckID]Check, len(checks))
	for _, c := range checks {
		byID[c.ID] = c
	}

	inDegree := make(map[CheckID]int, len(checks))
	adjList := make(map[CheckID][]CheckID, len(checks))

	for _, c := range checks {
		if _, exists := inDegree[c.ID]; !exists {
			inDegree[c.ID] = 0
		}
		for _, dep := range c.DependsOn {
			if _, ok := byID[dep]; !ok {
				return nil, ErrUnknownDependency
			}
			adjList[dep] = append(adjList[dep], c.ID)
			inDegree[c.ID]++
		}
	}

	return &graph{
		checks:   checks,
		inDegree: inDegree,
		adjList:  adjList,
		byID:     byID,
	}, nil
}

// topoSort returns checks grouped into parallel execution levels (Kahn's algorithm).
func (g *graph) topoSort() ([][]Check, error) {
	inDeg := make(map[CheckID]int, len(g.inDegree))
	for id, d := range g.inDegree {
		inDeg[id] = d
	}

	var queue []CheckID
	for _, c := range g.checks {
		if inDeg[c.ID] == 0 {
			queue = append(queue, c.ID)
		}
	}

	var levels [][]Check
	visited := 0

	for len(queue) > 0 {
		level := make([]Check, 0, len(queue))
		for _, id := range queue {
			level = append(level, g.byID[id])
		}
		levels = append(levels, level)
		visited += len(queue)

		var next []CheckID
		for _, id := range queue {
			for _, dep := range g.adjList[id] {
				inDeg[dep]--
				if inDeg[dep] == 0 {
					next = append(next, dep)
				}
			}
		}
		queue = next
	}

	if visited != len(g.checks) {
		return nil, ErrCycleDetected
	}

	return levels, nil
}
