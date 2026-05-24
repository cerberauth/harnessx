package harnessx

type Resource struct {
	ID       string
	URL      string
	Method   string
	Metadata map[string]string
}

type Target struct {
	URL      string
	Host     string
	Metadata map[string]string
	Data     any
}
