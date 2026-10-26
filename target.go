package harnessx

// Resource is one discoverable thing a ScopePerResource check runs against,
// such as an endpoint or an API operation. Checks emit resources in
// Result.Resources; per-resource checks that depend on them run once per
// resource.
//
// Resource IDs are expected to be unique across a run. Metadata holds string
// attributes and Data anything else a check wants to hand on (see
// ResourceDataAs).
type Resource struct {
	ID       string
	URL      string
	Method   string
	Metadata map[string]string
	Data     any
}

// Target is what a scan runs against. The engine never interprets it; it is
// passed unchanged to every check and reporter.
type Target struct {
	URL      string
	Host     string
	Metadata map[string]string
	Data     any
}
