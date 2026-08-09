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
	statusCode, header, body, duration, err := Do(context.Background(), http.DefaultClient, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if statusCode != http.StatusTeapot {
		t.Errorf("statusCode = %d, want %d", statusCode, http.StatusTeapot)
	}
	if header.Get("X-Test") != "value" {
		t.Errorf("header[X-Test] = %q, want %q", header.Get("X-Test"), "value")
	}
	if string(body) != "hello world" {
		t.Errorf("body = %q, want %q", body, "hello world")
	}
	if duration <= 0 {
		t.Error("duration should be positive")
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
	_, _, _, _, err := Do(context.Background(), http.DefaultClient, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody != wantBody {
		t.Errorf("server received body = %q, want %q", gotBody, wantBody)
	}
}

func TestDo_TransportError(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:0", nil)
	_, _, _, _, err := Do(context.Background(), http.DefaultClient, req)
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
	_, _, _, _, err := Do(ctx, http.DefaultClient, req)
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
	_, _, body, _, err := Do(context.Background(), http.DefaultClient, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("body = %q, want empty", body)
	}
}
