package probe

import (
	"context"
	"io"
	"net/http"
)

// RequestMutator mutates a *http.Request built by NewRequest, in place.
// Mutators are applied in the order given; the first error returned
// short-circuits the chain and is returned from NewRequest.
type RequestMutator func(*http.Request) error

// RequestBuilder builds a request for a single probe attempt.
type RequestBuilder func(ctx context.Context) (*http.Request, error)

// NewRequest builds an *http.Request via http.NewRequestWithContext and
// applies mutators to it in order. It is the single entry point checks
// should use instead of calling http.NewRequestWithContext directly.
func NewRequest(ctx context.Context, method, target string, body io.Reader, mutators ...RequestMutator) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	for _, m := range mutators {
		if m == nil {
			continue
		}
		if err := m(req); err != nil {
			return nil, err
		}
	}
	return req, nil
}

// WithHeader sets (overwrites) a single header value.
func WithHeader(name, value string) RequestMutator {
	return func(req *http.Request) error {
		req.Header.Set(name, value)
		return nil
	}
}

// WithCookie attaches a cookie to the request.
func WithCookie(cookie *http.Cookie) RequestMutator {
	return func(req *http.Request) error {
		req.AddCookie(cookie)
		return nil
	}
}

// WithQuery sets (overwrites) a single URL query parameter.
func WithQuery(name, value string) RequestMutator {
	return func(req *http.Request) error {
		q := req.URL.Query()
		q.Set(name, value)
		req.URL.RawQuery = q.Encode()
		return nil
	}
}
