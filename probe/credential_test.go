package probe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const testCredentialValue = "tok"
const testFormFieldName = "token"

func TestWithBearerToken_SetsAuthorizationHeader(t *testing.T) {
	req, err := NewRequest(context.Background(), "GET", "http://example.com", nil, WithBearerToken(testCredentialValue))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get(headerAuthorization); got != bearerPrefix+testCredentialValue {
		t.Errorf("Authorization = %q, want %q", got, bearerPrefix+testCredentialValue)
	}
}

func TestWithBasicAuth_SetsAuthorizationHeader(t *testing.T) {
	req, err := NewRequest(context.Background(), "GET", "http://example.com", nil, WithBasicAuth("alice", "hunter2"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	user, pass, ok := req.BasicAuth()
	if !ok {
		t.Fatal("BasicAuth() ok = false, want true")
	}
	if user != "alice" || pass != "hunter2" {
		t.Errorf("BasicAuth() = (%q, %q), want (%q, %q)", user, pass, "alice", "hunter2")
	}
}

func TestWithAPIKeyHeader_SetsHeader(t *testing.T) {
	req, err := NewRequest(context.Background(), "GET", "http://example.com", nil, WithAPIKeyHeader("X-API-Key", testCredentialValue))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("X-API-Key"); got != testCredentialValue {
		t.Errorf("X-API-Key = %q, want %q", got, testCredentialValue)
	}
}

func TestWithAPIKeyQuery_SetsQueryParam(t *testing.T) {
	req, err := NewRequest(context.Background(), "GET", "http://example.com", nil, WithAPIKeyQuery("access_token", testCredentialValue))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.URL.Query().Get("access_token"); got != testCredentialValue {
		t.Errorf("access_token = %q, want %q", got, testCredentialValue)
	}
}

func TestWithAuthCookie_SetsSecureCookie(t *testing.T) {
	req, err := NewRequest(context.Background(), "GET", "http://example.com", nil, WithAuthCookie("session", testCredentialValue))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cookie, err := req.Cookie("session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Value != testCredentialValue {
		t.Errorf("cookie value = %q, want %q", cookie.Value, testCredentialValue)
	}
}

func TestWithFormCredential_SetsPostBodyAndContentType(t *testing.T) {
	req, err := NewRequest(context.Background(), "GET", "http://example.com", nil, WithFormCredential(testFormFieldName, testCredentialValue))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Method != "POST" {
		t.Errorf("Method = %q, want %q", req.Method, "POST")
	}
	if got := req.Header.Get("Content-Type"); got != contentTypeForm {
		t.Errorf("Content-Type = %q, want %q", got, contentTypeForm)
	}
	if req.ContentLength == 0 {
		t.Error("ContentLength = 0, want > 0")
	}
	if req.GetBody == nil {
		t.Fatal("GetBody = nil, want a snapshot func")
	}
	body1, err := req.GetBody()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body2, err := req.GetBody()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := req.ParseForm(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.PostForm.Get(testFormFieldName); got != testCredentialValue {
		t.Errorf("token = %q, want %q", got, testCredentialValue)
	}
	_ = body1.Close()
	_ = body2.Close()
}

func TestMutators_ComposeViaNewRequest(t *testing.T) {
	req, err := NewRequest(context.Background(), "GET", "http://example.com", nil,
		WithHeader("X-Foo", "bar"),
		WithBearerToken(testCredentialValue),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("X-Foo"); got != "bar" {
		t.Errorf("X-Foo = %q, want %q", got, "bar")
	}
	if got := req.Header.Get(headerAuthorization); got != bearerPrefix+testCredentialValue {
		t.Errorf("Authorization = %q, want %q", got, bearerPrefix+testCredentialValue)
	}
}

func TestCredentialLocation_WithDefaults_EmptyDefaultsToBearerHeader(t *testing.T) {
	loc := CredentialLocation{}.WithDefaults()
	if loc.In != CredentialLocationHeader {
		t.Errorf("In = %q, want %q", loc.In, CredentialLocationHeader)
	}
	if loc.Name != headerAuthorization {
		t.Errorf("Name = %q, want %q", loc.Name, headerAuthorization)
	}
	if loc.Prefix != bearerPrefix {
		t.Errorf("Prefix = %q, want %q", loc.Prefix, bearerPrefix)
	}
}

func TestCredentialLocation_WithDefaults_NonHeaderGetsGenericName(t *testing.T) {
	loc := CredentialLocation{In: CredentialLocationCookie}.WithDefaults()
	if loc.Name != "token" {
		t.Errorf("Name = %q, want %q", loc.Name, "token")
	}
	if loc.Prefix != "" {
		t.Errorf("Prefix = %q, want empty", loc.Prefix)
	}
}

func TestCredentialLocation_Validate(t *testing.T) {
	valid := []string{"", CredentialLocationHeader, CredentialLocationCookie, CredentialLocationQuery, CredentialLocationBody}
	for _, in := range valid {
		if err := (CredentialLocation{In: in}).Validate(); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", in, err)
		}
	}
	if err := (CredentialLocation{In: "bogus"}).Validate(); err == nil {
		t.Error("Validate(\"bogus\") = nil, want error")
	}
}

func TestNewCredentialRequest_HeaderPlacement(t *testing.T) {
	loc := CredentialLocation{In: CredentialLocationHeader, Name: headerAuthorization, Prefix: bearerPrefix}
	req, err := NewCredentialRequest(context.Background(), "http://example.com", testCredentialValue, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Method != "GET" {
		t.Errorf("Method = %q, want %q", req.Method, "GET")
	}
	if got := req.Header.Get(headerAuthorization); got != bearerPrefix+testCredentialValue {
		t.Errorf("Authorization = %q, want %q", got, bearerPrefix+testCredentialValue)
	}
}

func TestNewCredentialRequest_CookiePlacement(t *testing.T) {
	loc := CredentialLocation{In: CredentialLocationCookie, Name: "session"}
	req, err := NewCredentialRequest(context.Background(), "http://example.com", testCredentialValue, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cookie, err := req.Cookie("session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Value != testCredentialValue {
		t.Errorf("cookie value = %q, want %q", cookie.Value, testCredentialValue)
	}
}

func TestNewCredentialRequest_QueryPlacement(t *testing.T) {
	loc := CredentialLocation{In: CredentialLocationQuery, Name: "access_token"}
	req, err := NewCredentialRequest(context.Background(), "http://example.com", testCredentialValue, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.URL.Query().Get("access_token"); got != testCredentialValue {
		t.Errorf("access_token = %q, want %q", got, testCredentialValue)
	}
}

func TestNewCredentialRequest_BodyPlacement(t *testing.T) {
	loc := CredentialLocation{In: CredentialLocationBody, Name: testFormFieldName}
	req, err := NewCredentialRequest(context.Background(), "http://example.com", testCredentialValue, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Method != "POST" {
		t.Errorf("Method = %q, want %q", req.Method, "POST")
	}
	if err := req.ParseForm(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.PostForm.Get(testFormFieldName); got != testCredentialValue {
		t.Errorf("%s = %q, want %q", testFormFieldName, got, testCredentialValue)
	}
}

func TestNewCredentialRequest_DefaultPlacementIsHeader(t *testing.T) {
	loc := CredentialLocation{Name: "X-Api-Key"}
	req, err := NewCredentialRequest(context.Background(), "http://example.com", testCredentialValue, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("X-Api-Key"); got != testCredentialValue {
		t.Errorf("X-Api-Key = %q, want %q", got, testCredentialValue)
	}
}

func TestNewCredentialRequest_InvalidTarget_ReturnsError(t *testing.T) {
	_, err := NewCredentialRequest(context.Background(), "://bad-url", testCredentialValue, CredentialLocation{})
	if err == nil {
		t.Error("expected error, got nil")
	}
}

const (
	applyBaseURL    = "http://example.com/items?page=2"
	applyBaseBody   = `{"name":"widget"}`
	applyTraceName  = "X-Trace"
	applyTraceValue = "trace-1"
	applyOldAuth    = "Basic b2xkOm9sZA=="
	applySession    = "session"
)

// newApplyBase returns a POST with a JSON body, query, header and cookie, so
// every placement can be checked against what the caller already set.
func newApplyBase(t *testing.T) *http.Request {
	t.Helper()
	req, err := NewRequest(context.Background(), http.MethodPost, applyBaseURL, strings.NewReader(applyBaseBody),
		WithHeader("Content-Type", "application/json"),
		WithHeader(applyTraceName, applyTraceValue),
		WithHeader(headerAuthorization, applyOldAuth),
		WithAuthCookie("pref", "dark"),
	)
	if err != nil {
		t.Fatalf("building base request: %v", err)
	}
	return req
}

func readBody(t *testing.T, req *http.Request) string {
	t.Helper()
	b, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return string(b)
}

// assertBaseUntouched checks that ApplyCredential left the caller's request as built.
func assertBaseUntouched(t *testing.T, base *http.Request) {
	t.Helper()
	if base.Method != http.MethodPost {
		t.Errorf("base Method = %q, want POST", base.Method)
	}
	if got := base.URL.String(); got != applyBaseURL {
		t.Errorf("base URL = %q, want %q", got, applyBaseURL)
	}
	if got := base.Header.Get(headerAuthorization); got != applyOldAuth {
		t.Errorf("base Authorization = %q, want %q", got, applyOldAuth)
	}
	if _, err := base.Cookie(applySession); err == nil {
		t.Error("base gained a session cookie")
	}
	if got := readBody(t, base); got != applyBaseBody {
		t.Errorf("base body = %q, want %q", got, applyBaseBody)
	}
}

func TestApplyCredential_HeaderKeepsMethodBodyAndOtherHeaders(t *testing.T) {
	base := newApplyBase(t)
	out, err := ApplyCredential(base, testCredentialValue, CredentialLocation{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", out.Method)
	}
	if got := out.Header.Get(headerAuthorization); got != bearerPrefix+testCredentialValue {
		t.Errorf("Authorization = %q, want %q (existing value replaced)", got, bearerPrefix+testCredentialValue)
	}
	if got := out.Header.Get(applyTraceName); got != applyTraceValue {
		t.Errorf("%s = %q, want %q", applyTraceName, got, applyTraceValue)
	}
	if got := out.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := out.URL.String(); got != applyBaseURL {
		t.Errorf("URL = %q, want %q", got, applyBaseURL)
	}
	if got := readBody(t, out); got != applyBaseBody {
		t.Errorf("body = %q, want %q", got, applyBaseBody)
	}
	assertBaseUntouched(t, base)
}

func TestApplyCredential_CustomHeaderWithPrefix(t *testing.T) {
	base := newApplyBase(t)
	out, err := ApplyCredential(base, testCredentialValue, CredentialLocation{Name: "X-Api-Key", Prefix: "k-"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := out.Header.Get("X-Api-Key"); got != "k-"+testCredentialValue {
		t.Errorf("X-Api-Key = %q, want %q", got, "k-"+testCredentialValue)
	}
	if got := out.Header.Get(headerAuthorization); got != applyOldAuth {
		t.Errorf("unrelated Authorization changed to %q", got)
	}
	if base.Header.Get("X-Api-Key") != "" {
		t.Error("base gained X-Api-Key")
	}
}

func TestApplyCredential_QueryKeepsExistingParams(t *testing.T) {
	base := newApplyBase(t)
	out, err := ApplyCredential(base, testCredentialValue, CredentialLocation{In: CredentialLocationQuery, Name: "access_token"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	q := out.URL.Query()
	if q.Get("access_token") != testCredentialValue || q.Get("page") != "2" {
		t.Errorf("query = %v, want access_token and page=2", q)
	}
	if out.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", out.Method)
	}
	if got := readBody(t, out); got != applyBaseBody {
		t.Errorf("body = %q, want %q", got, applyBaseBody)
	}
	assertBaseUntouched(t, base)
}

func TestApplyCredential_CookieKeepsExistingCookies(t *testing.T) {
	base := newApplyBase(t)
	out, err := ApplyCredential(base, testCredentialValue, CredentialLocation{In: CredentialLocationCookie, Name: applySession})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if c, err := out.Cookie(applySession); err != nil || c.Value != testCredentialValue {
		t.Errorf("session cookie = %v, %v; want value %q", c, err, testCredentialValue)
	}
	if c, err := out.Cookie("pref"); err != nil || c.Value != "dark" {
		t.Errorf("existing pref cookie = %v, %v; want dark", c, err)
	}
	if got := readBody(t, out); got != applyBaseBody {
		t.Errorf("body = %q, want %q", got, applyBaseBody)
	}
	assertBaseUntouched(t, base)
}

func TestApplyCredential_BodyReplacesBodyWithFormAndSwitchesToPost(t *testing.T) {
	base := newApplyBase(t)
	base.Method = http.MethodPut
	out, err := ApplyCredential(base, testCredentialValue, CredentialLocation{In: CredentialLocationBody, Name: testFormFieldName})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", out.Method)
	}
	if got := out.Header.Get("Content-Type"); got != contentTypeForm {
		t.Errorf("Content-Type = %q, want %q", got, contentTypeForm)
	}
	if got := readBody(t, out); got != testFormFieldName+"="+testCredentialValue {
		t.Errorf("body = %q, want the form field only", got)
	}
	if base.Method != http.MethodPut {
		t.Errorf("base Method = %q, want PUT", base.Method)
	}
	if got := readBody(t, base); got != applyBaseBody {
		t.Errorf("base body = %q, want %q", got, applyBaseBody)
	}
}

func TestApplyCredential_ResultBodyIsReplayable(t *testing.T) {
	base := newApplyBase(t)
	out, err := ApplyCredential(base, testCredentialValue, CredentialLocation{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = readBody(t, out) // consume it once

	again, err := out.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	if b, _ := io.ReadAll(again); string(b) != applyBaseBody {
		t.Errorf("replayed body = %q, want %q", b, applyBaseBody)
	}
}

func TestApplyCredential_BodyWithoutGetBodyIsBufferedAndRestored(t *testing.T) {
	base, err := http.NewRequest(http.MethodPost, applyBaseURL, io.NopCloser(strings.NewReader(applyBaseBody)))
	if err != nil {
		t.Fatal(err)
	}
	if base.GetBody != nil {
		t.Fatal("test setup: expected a request without GetBody")
	}

	out, err := ApplyCredential(base, testCredentialValue, CredentialLocation{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readBody(t, out); got != applyBaseBody {
		t.Errorf("out body = %q, want %q", got, applyBaseBody)
	}
	if got := readBody(t, base); got != applyBaseBody {
		t.Errorf("base body = %q after ApplyCredential, want it restored", got)
	}
	if out.GetBody == nil {
		t.Error("out.GetBody = nil, want replayable")
	}
}

func TestApplyCredential_NoBody(t *testing.T) {
	base, _ := NewRequest(context.Background(), http.MethodGet, applyBaseURL, nil)
	out, err := ApplyCredential(base, testCredentialValue, CredentialLocation{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Method != http.MethodGet || out.Body != nil && out.Body != http.NoBody {
		t.Errorf("Method = %q Body = %v, want GET with no body", out.Method, out.Body)
	}
}

func TestApplyCredential_InvalidLocation(t *testing.T) {
	_, err := ApplyCredential(newApplyBase(t), testCredentialValue, CredentialLocation{In: "bogus"})
	if err == nil {
		t.Error("expected error for unknown In, got nil")
	}
}

func TestApplyCredential_GetBodyError(t *testing.T) {
	base := newApplyBase(t)
	errBoom := errors.New("boom")
	base.GetBody = func() (io.ReadCloser, error) { return nil, errBoom }

	_, err := ApplyCredential(base, testCredentialValue, CredentialLocation{})
	if !errors.Is(err, errBoom) {
		t.Errorf("error = %v, want %v", err, errBoom)
	}
}
