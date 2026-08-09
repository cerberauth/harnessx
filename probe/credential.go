package probe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	headerAuthorization = "Authorization"
	bearerPrefix        = "Bearer "
	contentTypeForm     = "application/x-www-form-urlencoded"
)

// WithBearerToken sets the Authorization header to "Bearer <token>".
func WithBearerToken(token string) RequestMutator {
	return WithHeader(headerAuthorization, bearerPrefix+token)
}

// WithBasicAuth sets HTTP Basic authentication credentials on the request.
func WithBasicAuth(username, password string) RequestMutator {
	return func(req *http.Request) error {
		req.SetBasicAuth(username, password)
		return nil
	}
}

// WithAPIKeyHeader sets an arbitrary named header to value, e.g. a custom
// API-key header such as X-API-Key.
func WithAPIKeyHeader(name, value string) RequestMutator {
	return WithHeader(name, value)
}

// WithAPIKeyQuery sets an arbitrary named query parameter to value.
func WithAPIKeyQuery(name, value string) RequestMutator {
	return WithQuery(name, value)
}

// WithAuthCookie attaches a session/auth cookie with secure defaults
// (Secure, HttpOnly, SameSite=Strict).
func WithAuthCookie(name, value string) RequestMutator {
	return WithCookie(&http.Cookie{
		Name:     name,
		Value:    value,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// WithFormCredential places name=value as a urlencoded form body field,
// switching the request method to POST.
func WithFormCredential(name, value string) RequestMutator {
	return func(req *http.Request) error {
		form := url.Values{}
		form.Set(name, value)
		encoded := form.Encode()
		req.Method = http.MethodPost
		req.Body = io.NopCloser(strings.NewReader(encoded))
		req.ContentLength = int64(len(encoded))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(encoded)), nil
		}
		return WithHeader("Content-Type", contentTypeForm)(req)
	}
}

// CredentialLocation describes where to place a credential value in an HTTP
// request: a header (default), a cookie, a query parameter, or a
// form-encoded body field. It's the declarative counterpart to the With*
// mutators above, for callers (e.g. a security check) that pick the
// placement at runtime instead of hardcoding one mutator.
type CredentialLocation struct {
	In     string // CredentialLocationHeader (default), *Cookie, *Query, or *Body
	Name   string // header/cookie/query/form-field name
	Prefix string // value prefix, e.g. "Bearer "
}

const (
	CredentialLocationHeader = "header"
	CredentialLocationCookie = "cookie"
	CredentialLocationQuery  = "query"
	CredentialLocationBody   = "body"
)

// WithDefaults fills unset fields, defaulting to an Authorization: Bearer
// header when nothing is overridden.
func (l CredentialLocation) WithDefaults() CredentialLocation {
	if l.In == "" {
		l.In = CredentialLocationHeader
	}
	if l.Name == "" {
		if l.In == CredentialLocationHeader {
			l.Name = headerAuthorization
		} else {
			l.Name = "token"
		}
	}
	if l.Prefix == "" && l.In == CredentialLocationHeader && strings.EqualFold(l.Name, headerAuthorization) {
		l.Prefix = bearerPrefix
	}
	return l
}

// Validate rejects an unknown In value.
func (l CredentialLocation) Validate() error {
	switch l.In {
	case "", CredentialLocationHeader, CredentialLocationCookie, CredentialLocationQuery, CredentialLocationBody:
		return nil
	default:
		return fmt.Errorf("probe: invalid credential location %q: must be one of header, cookie, query, body", l.In)
	}
}

// NewCredentialRequest builds a request against target with value placed at
// loc. Header, cookie, and query placements produce a GET; body placement
// switches to POST via WithFormCredential.
func NewCredentialRequest(ctx context.Context, target string, value string, loc CredentialLocation) (*http.Request, error) {
	value = loc.Prefix + value
	var mutator RequestMutator
	switch loc.In {
	case CredentialLocationCookie:
		mutator = WithAuthCookie(loc.Name, value)
	case CredentialLocationQuery:
		mutator = WithAPIKeyQuery(loc.Name, value)
	case CredentialLocationBody:
		mutator = WithFormCredential(loc.Name, value)
	default:
		mutator = WithAPIKeyHeader(loc.Name, value)
	}
	return NewRequest(ctx, http.MethodGet, target, nil, mutator)
}
