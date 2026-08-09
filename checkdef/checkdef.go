// Package checkdef parses declarative check metadata (ID, name,
// description, dependencies, ...) out of an embedded definition file, so a
// reusable check package can keep that metadata in YAML/TOML/JSON instead
// of hand-assembling it in Go. It's a separate package from the root
// harnessx module so the core engine keeps its zero-dependency guarantee —
// only importers that actually parse definition files pull in a YAML/TOML
// parser.
package checkdef

import "github.com/cerberauth/harnessx"

// CheckDef mirrors the descriptive fields of harnessx.Check (everything
// except behavior — Skip, Run, RunResource, Scope, Timeout, Concurrency).
type CheckDef struct {
	ID          string   `yaml:"id" toml:"id" json:"id"`
	Name        string   `yaml:"name" toml:"name" json:"name"`
	Description string   `yaml:"description" toml:"description" json:"description"`
	Link        string   `yaml:"link" toml:"link" json:"link"`
	Tags        []string `yaml:"tags" toml:"tags" json:"tags"`
	DependsOn   []string `yaml:"depends_on" toml:"depends_on" json:"depends_on"`
}

// DependsOnIDs converts DependsOn to []harnessx.CheckID for direct use in
// Check.DependsOn.
func (d CheckDef) DependsOnIDs() []harnessx.CheckID {
	ids := make([]harnessx.CheckID, len(d.DependsOn))
	for i, s := range d.DependsOn {
		ids[i] = harnessx.CheckID(s)
	}
	return ids
}
