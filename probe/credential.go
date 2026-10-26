package probe

import (
	"bytes"
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

// NewCredentialRequest builds a GET request against target and places value at
// loc on it via ApplyCredential, so loc is defaulted and validated the same
// way. Header, cookie, and query placements keep the GET; body placement
// switches to POST via WithFormCredential.
func NewCredentialRequest(ctx context.Context, target string, value string, loc CredentialLocation) (*http.Request, error) {
	req, err := NewRequest(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	return ApplyCredential(req, value, loc)
}

// ApplyCredential returns a copy of req with loc.Prefix+value placed at loc,
// leaving the caller's method, URL, headers, cookies and body intact — the
// way to inject a credential into a request the caller already built.
//
// loc is first completed with CredentialLocation.WithDefaults and checked with
// CredentialLocation.Validate. The credential then goes where loc.In says:
//   - header: the header loc.Name is set, replacing any existing value;
//   - cookie: a cookie loc.Name is set (WithAuthCookie), replacing any existing
//     cookie of that name;
//   - query: the query parameter loc.Name is set, replacing any existing value;
//   - body: with the semantics of WithFormCredential — the method becomes POST
//     and the body is replaced by a urlencoded form holding only loc.Name.
//
// req is never modified. The copy's body is an independent reader over the same
// bytes (via req.GetBody), so both stay replayable. A request with a body but no
// GetBody is buffered in memory once; req.Body is then restored to a fresh
// reader over those bytes.
func ApplyCredential(req *http.Request, value string, loc CredentialLocation) (*http.Request, error) {
	loc = loc.WithDefaults()
	if err := loc.Validate(); err != nil {
		return nil, err
	}

	out := req.Clone(req.Context())
	if err := cloneBody(req, out); err != nil {
		return nil, err
	}

	value = loc.Prefix + value
	var mutator RequestMutator
	switch loc.In {
	case CredentialLocationCookie:
		removeCookie(out, loc.Name)
		mutator = WithAuthCookie(loc.Name, value)
	case CredentialLocationQuery:
		mutator = WithAPIKeyQuery(loc.Name, value)
	case CredentialLocationBody:
		mutator = WithFormCredential(loc.Name, value)
	default:
		mutator = WithAPIKeyHeader(loc.Name, value)
	}
	if err := mutator(out); err != nil {
		return nil, err
	}
	return out, nil
}

// removeCookie drops every cookie called name from req's Cookie header, keeping
// the others, so that adding one with the same name replaces it rather than
// sending the name twice.
func removeCookie(req *http.Request, name string) {
	cookies := req.Cookies()
	req.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name != name {
			req.AddCookie(c)
		}
	}
}

// cloneBody gives dst, a Clone of src, its own body reader. Clone shares the
// Body reader with the original, so without this the two would consume one stream.
func cloneBody(src, dst *http.Request) error {
	if src.Body == nil || src.Body == http.NoBody {
		return nil
	}
	if src.GetBody != nil {
		body, err := src.GetBody()
		if err != nil {
			return fmt.Errorf("probe: cloning request body: %w", err)
		}
		dst.Body = body
		return nil
	}

	data, err := io.ReadAll(src.Body)
	_ = src.Body.Close()
	if err != nil {
		return fmt.Errorf("probe: reading request body: %w", err)
	}
	src.Body = io.NopCloser(bytes.NewReader(data))
	dst.Body = io.NopCloser(bytes.NewReader(data))
	dst.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return nil
}
