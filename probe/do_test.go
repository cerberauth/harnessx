package probe

import (
	"bytes"
	"context"
	"errors"
	"io"
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

func TestDoResponse_SmallBodyNotTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("small"))
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := DoResponse(context.Background(), http.DefaultClient, req, WithMaxBodySize(1024))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(resp.Body) != "small" || resp.Truncated {
		t.Errorf("body = %q truncated = %v, want %q false", resp.Body, resp.Truncated, "small")
	}
}

func TestDoResponse_ExactlyAtCapNotTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), 100))
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := DoResponse(context.Background(), http.DefaultClient, req, WithMaxBodySize(100))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Body) != 100 || resp.Truncated {
		t.Errorf("len = %d truncated = %v, want 100 false", len(resp.Body), resp.Truncated)
	}
}

func TestDoResponse_LargeBodyTruncated(t *testing.T) {
	const size = 1 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := bytes.Repeat([]byte("x"), 4096)
		for sent := 0; sent < size; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := DoResponse(context.Background(), http.DefaultClient, req, WithMaxBodySize(10_000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Body) != 10_000 {
		t.Errorf("len(body) = %d, want 10000", len(resp.Body))
	}
	if !resp.Truncated {
		t.Error("Truncated = false, want true")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestDoResponse_UnlimitedWhenNonPositive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), 5000))
	}))
	defer srv.Close()

	for _, n := range []int64{0, -1} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		resp, err := DoResponse(context.Background(), http.DefaultClient, req, WithMaxBodySize(n))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.Body) != 5000 || resp.Truncated {
			t.Errorf("n=%d: len = %d truncated = %v, want 5000 false", n, len(resp.Body), resp.Truncated)
		}
	}
}

func TestDo_DefaultCap(t *testing.T) {
	over := DefaultMaxBodySize + 1024
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.CopyN(w, zeroReader{}, int64(over))
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, _, body, _, err := Do(context.Background(), http.DefaultClient, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(body) != DefaultMaxBodySize {
		t.Errorf("len(body) = %d, want %d", len(body), DefaultMaxBodySize)
	}

	req, _ = http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := DoResponse(context.Background(), http.DefaultClient, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Truncated {
		t.Error("Truncated = false, want true for default cap")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
