package harnessx

import (
	"context"
	"net/http"

	"github.com/cerberauth/harnessx/probe"
)

// NewRequestFromResource builds a request for resource r via probe.NewRequest,
// defaulting to GET when r.Method is empty.
func NewRequestFromResource(ctx context.Context, r Resource, mutators ...probe.RequestMutator) (*http.Request, error) {
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	return probe.NewRequest(ctx, method, r.URL, nil, mutators...)
}
