package probe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDo_CapturesStatusHeaderBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "value")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	a, err := Do(context.Background(), http.DefaultClient, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.StatusCode != http.StatusTeapot {
		t.Errorf("StatusCode = %d, want %d", a.StatusCode, http.StatusTeapot)
	}
	if a.Header.Get("X-Test") != "value" {
		t.Errorf("Header[X-Test] = %q, want %q", a.Header.Get("X-Test"), "value")
	}
	if string(a.Body) != "hello world" {
		t.Errorf("Body = %q, want %q", a.Body, "hello world")
	}
	if a.Duration <= 0 {
		t.Error("Duration should be positive")
	}
}

func TestDo_RequestBodyReplayable(t *testing.T) {
	const wantBody = "request payload"
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, len(wantBody))
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(wantBody))
	_, err := Do(context.Background(), http.DefaultClient, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody != wantBody {
		t.Errorf("server received body = %q, want %q", gotBody, wantBody)
	}
}

func TestDo_TransportError(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:0", nil)
	_, err := Do(context.Background(), http.DefaultClient, req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDo_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := Do(ctx, http.DefaultClient, req)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestDo_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	a, err := Do(context.Background(), http.DefaultClient, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.Body) != 0 {
		t.Errorf("Body = %q, want empty", a.Body)
	}
}
