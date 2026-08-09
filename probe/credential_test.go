package probe

import (
	"context"
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
