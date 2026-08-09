package probe

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestNewRequest_AppliesMutatorsInOrder(t *testing.T) {
	var order []int
	mutator := func(n int) RequestMutator {
		return func(req *http.Request) error {
			order = append(order, n)
			return nil
		}
	}
	_, err := NewRequest(context.Background(), "GET", "http://example.com", nil, mutator(1), mutator(2), mutator(3))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("order = %v, want [1 2 3]", order)
	}
}

func TestNewRequest_MutatorErrorShortCircuits(t *testing.T) {
	errBoom := errors.New("boom")
	var ran bool
	failing := func(req *http.Request) error { return errBoom }
	after := func(req *http.Request) error { ran = true; return nil }

	_, err := NewRequest(context.Background(), "GET", "http://example.com", nil, failing, after)
	if !errors.Is(err, errBoom) {
		t.Errorf("err = %v, want %v", err, errBoom)
	}
	if ran {
		t.Error("mutator after the failing one ran, want short-circuit")
	}
}

func TestNewRequest_InvalidTarget_ReturnsError(t *testing.T) {
	_, err := NewRequest(context.Background(), "GET", "://bad-url", nil)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestWithHeader_SetsAndOverwrites(t *testing.T) {
	req, err := NewRequest(context.Background(), "GET", "http://example.com", nil,
		WithHeader("X-Foo", "one"),
		WithHeader("X-Foo", "two"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("X-Foo"); got != "two" {
		t.Errorf("X-Foo = %q, want %q", got, "two")
	}
}

func TestWithQuery_SetsAndOverwrites(t *testing.T) {
	req, err := NewRequest(context.Background(), "GET", "http://example.com", nil,
		WithQuery("q", "one"),
		WithQuery("q", "two"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.URL.Query().Get("q"); got != "two" {
		t.Errorf("q = %q, want %q", got, "two")
	}
}

func TestWithCookie_Attaches(t *testing.T) {
	req, err := NewRequest(context.Background(), "GET", "http://example.com", nil,
		WithCookie(&http.Cookie{Name: "session", Value: "abc", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cookie, err := req.Cookie("session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Value != "abc" {
		t.Errorf("cookie value = %q, want %q", cookie.Value, "abc")
	}
}
