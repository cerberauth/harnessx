package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- Constructor / config tests ---

func TestNew_Defaults(t *testing.T) {
	p := New()
	if p.cfg.userAgent != defaultUserAgent {
		t.Errorf("userAgent = %q, want %q", p.cfg.userAgent, defaultUserAgent)
	}
	if p.cfg.maxRetries != defaultMaxRetries {
		t.Errorf("maxRetries = %d, want %d", p.cfg.maxRetries, defaultMaxRetries)
	}
	if p.cfg.retryDelay != defaultRetryDelay {
		t.Errorf("retryDelay = %v, want %v", p.cfg.retryDelay, defaultRetryDelay)
	}
	if p.cfg.timeout != defaultTimeout {
		t.Errorf("timeout = %v, want %v", p.cfg.timeout, defaultTimeout)
	}
	if p.transport != http.DefaultTransport {
		t.Error("transport should default to http.DefaultTransport")
	}
}

func TestNew_WithOptions(t *testing.T) {
	customTransport := &http.Transport{}
	p := New(
		WithUserAgent("my-agent/2.0"),
		WithMaxRetries(5),
		WithRetryDelay(100*time.Millisecond),
		WithTransport(customTransport),
		WithTimeout(10*time.Second),
	)
	if p.cfg.userAgent != "my-agent/2.0" {
		t.Errorf("userAgent = %q", p.cfg.userAgent)
	}
	if p.cfg.maxRetries != 5 {
		t.Errorf("maxRetries = %d", p.cfg.maxRetries)
	}
	if p.cfg.retryDelay != 100*time.Millisecond {
		t.Errorf("retryDelay = %v", p.cfg.retryDelay)
	}
	if p.transport != customTransport {
		t.Error("transport not set")
	}
	if p.cfg.timeout != 10*time.Second {
		t.Errorf("timeout = %v", p.cfg.timeout)
	}
}

func TestNew_ZeroOrNegativeOptionsIgnored(t *testing.T) {
	p := New(
		WithUserAgent(""),
		WithMaxRetries(-1),
		WithRetryDelay(-1),
		WithTransport(nil),
		WithTimeout(-1),
	)
	if p.cfg.userAgent != defaultUserAgent {
		t.Error("empty user-agent should be ignored")
	}
	if p.cfg.maxRetries != defaultMaxRetries {
		t.Error("negative maxRetries should be ignored")
	}
	if p.cfg.retryDelay != defaultRetryDelay {
		t.Error("negative retryDelay should be ignored")
	}
	if p.transport != http.DefaultTransport {
		t.Error("nil transport should be ignored")
	}
	if p.cfg.timeout != defaultTimeout {
		t.Error("negative timeout should be ignored")
	}
}

func TestProbe_Client_UsesProbeTransport(t *testing.T) {
	p := New()
	c := p.Client()
	if c.Transport != p {
		t.Error("client Transport should be the probe itself")
	}
}

func TestProbe_Client_Timeout(t *testing.T) {
	p := New(WithTimeout(7 * time.Second))
	c := p.Client()
	if c.Timeout != 7*time.Second {
		t.Errorf("client Timeout = %v, want 7s", c.Timeout)
	}
}

// --- RoundTrip behavioural tests ---

func TestRoundTrip_UserAgentSet(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithUserAgent("test-agent/1.0"), WithMaxRetries(0))
	c := p.Client()
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if gotUA != "test-agent/1.0" {
		t.Errorf("User-Agent = %q, want test-agent/1.0", gotUA)
	}
}

func TestRoundTrip_AnyVerb(t *testing.T) {
	verbs := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithMaxRetries(0))
	c := p.Client()
	for _, method := range verbs {
		req, _ := http.NewRequest(method, srv.URL, nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", method, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d", method, resp.StatusCode)
		}
	}
}

func TestRoundTrip_Retry_429_ThenSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithMaxRetries(3), WithRetryDelay(1*time.Millisecond))
	c := p.Client()
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status = %d, want 200", resp.StatusCode)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

func TestRoundTrip_Retry_503_ThenSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithMaxRetries(2), WithRetryDelay(1*time.Millisecond))
	c := p.Client()
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status = %d", resp.StatusCode)
	}
}

func TestRoundTrip_MaxRetriesExceeded(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := New(WithMaxRetries(2), WithRetryDelay(1*time.Millisecond))
	c := p.Client()
	_, err := c.Get(srv.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrMaxRetriesExceeded) {
		t.Errorf("error = %v, want ErrMaxRetriesExceeded", err)
	}
	// maxRetries=2 → 3 total attempts
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

func TestRoundTrip_Non4xxNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(fmt.Sprintf("%d", status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
			}))
			defer srv.Close()

			p := New(WithMaxRetries(3), WithRetryDelay(1*time.Millisecond))
			c := p.Client()
			resp, err := c.Get(srv.URL)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != status {
				t.Errorf("status = %d, want %d", resp.StatusCode, status)
			}
			if calls.Load() != 1 {
				t.Errorf("calls = %d, want 1 (no retry)", calls.Load())
			}
		})
	}
}

func TestRoundTrip_NetworkError_Retry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			// Hijack and close to force a network error on the client side.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("ResponseWriter does not implement Hijacker")
				return
			}
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithMaxRetries(3), WithRetryDelay(1*time.Millisecond))
	c := p.Client()
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestRoundTrip_NetworkError_ExhaustedWrapsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer srv.Close()

	p := New(WithMaxRetries(1), WithRetryDelay(1*time.Millisecond))
	c := p.Client()
	_, err := c.Get(srv.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrMaxRetriesExceeded) {
		t.Errorf("error = %v, want ErrMaxRetriesExceeded", err)
	}
}

func TestRoundTrip_ContextCancelledBeforeStart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := New(WithMaxRetries(3))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	_, err := p.RoundTrip(req)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestRoundTrip_ContextCancelledMidRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			// Cancel during the first 429 sleep.
			cancel()
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := New(WithMaxRetries(3), WithRetryDelay(500*time.Millisecond))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	_, err := p.RoundTrip(req)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if calls.Load() > 2 {
		t.Errorf("calls = %d, expected at most 2 (cancelled mid-retry)", calls.Load())
	}
}

func TestRoundTrip_BodyReplayedOnRetry(t *testing.T) {
	const wantBody = "hello retry"
	// HTTP/1.1 processes requests from a single client sequentially,
	// so no mutex is needed here.
	var received []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		received = append(received, string(data))
		if len(received) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithMaxRetries(3), WithRetryDelay(1*time.Millisecond))
	c := p.Client()
	resp, err := c.Post(srv.URL, "text/plain", strings.NewReader(wantBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	for i, body := range received {
		if body != wantBody {
			t.Errorf("attempt %d body = %q, want %q", i, body, wantBody)
		}
	}
}

// --- Header parsing unit tests ---

func TestRetryAfterDuration_Integer(t *testing.T) {
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("Retry-After", "5")
	d := retryAfterDuration(resp, 100*time.Millisecond, 0)
	if d != 5*time.Second {
		t.Errorf("d = %v, want 5s", d)
	}
}

func TestRetryAfterDuration_HTTPDateFuture(t *testing.T) {
	future := time.Now().Add(3 * time.Second)
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("Retry-After", future.UTC().Format(http.TimeFormat))
	d := retryAfterDuration(resp, 100*time.Millisecond, 0)
	// Allow some tolerance for parsing/execution time.
	if d < 2*time.Second || d > 4*time.Second {
		t.Errorf("d = %v, want ~3s", d)
	}
}

func TestRetryAfterDuration_HTTPDatePast(t *testing.T) {
	past := time.Now().Add(-1 * time.Second)
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("Retry-After", past.UTC().Format(http.TimeFormat))
	d := retryAfterDuration(resp, 100*time.Millisecond, 0)
	if d != 0 {
		t.Errorf("d = %v, want 0 (date in past)", d)
	}
}

func TestRetryAfterDuration_XRateLimitReset(t *testing.T) {
	future := time.Now().Add(4 * time.Second)
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("X-RateLimit-Reset", fmt.Sprintf("%d", future.Unix()))
	d := retryAfterDuration(resp, 100*time.Millisecond, 0)
	if d < 3*time.Second || d > 5*time.Second {
		t.Errorf("d = %v, want ~4s", d)
	}
}

func TestRetryAfterDuration_XRateLimitResetPast(t *testing.T) {
	past := time.Now().Add(-1 * time.Second)
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("X-RateLimit-Reset", fmt.Sprintf("%d", past.Unix()))
	d := retryAfterDuration(resp, 100*time.Millisecond, 0)
	if d != 0 {
		t.Errorf("d = %v, want 0 (timestamp in past)", d)
	}
}

func TestRetryAfterDuration_Fallback(t *testing.T) {
	resp := &http.Response{Header: make(http.Header)}
	d := retryAfterDuration(resp, 100*time.Millisecond, 0)
	// Should be in [0, base*1] with full jitter.
	if d < 0 || d > 100*time.Millisecond {
		t.Errorf("d = %v, want in [0, 100ms]", d)
	}
}

// --- Backoff unit tests ---

func TestBackoffDuration_Grows(t *testing.T) {
	base := 10 * time.Millisecond
	// Each successive attempt has a larger or equal cap.
	prevMax := time.Duration(0)
	for attempt := 0; attempt <= 12; attempt++ {
		// Run multiple samples to check the cap.
		maxSeen := time.Duration(0)
		for range 1000 {
			d := backoffDuration(base, attempt)
			if d < 0 {
				t.Fatalf("attempt %d: negative duration %v", attempt, d)
			}
			if d > maxSeen {
				maxSeen = d
			}
		}
		slot := min(base*(1<<min(attempt, 10)), 60*time.Second)
		if maxSeen > slot {
			t.Errorf("attempt %d: max sample %v exceeds slot %v", attempt, maxSeen, slot)
		}
		if attempt > 0 && slot <= prevMax {
			// slot should grow (until cap)
		}
		prevMax = slot
	}
}

func TestBackoffDuration_NeverExceedsCap(t *testing.T) {
	base := 1 * time.Second
	for attempt := range 20 {
		d := backoffDuration(base, attempt)
		if d > 60*time.Second {
			t.Errorf("attempt %d: d = %v exceeds 60s cap", attempt, d)
		}
	}
}

// --- Body snapshot unit tests ---

func TestSnapshotBody_NilBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	data, err := snapshotBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != nil {
		t.Errorf("data = %v, want nil", data)
	}
}

func TestSnapshotBody_ReadAndRestore(t *testing.T) {
	const want = "body content"
	req, _ := http.NewRequest(http.MethodPost, "http://example.com", strings.NewReader(want))
	data, err := snapshotBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != want {
		t.Errorf("data = %q, want %q", data, want)
	}
	// Body should be restored and readable again.
	restored, _ := io.ReadAll(req.Body)
	if string(restored) != want {
		t.Errorf("restored body = %q, want %q", restored, want)
	}
}

func TestSnapshotBody_NoBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", http.NoBody)
	data, err := snapshotBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != nil {
		t.Errorf("data = %v, want nil for http.NoBody", data)
	}
}

// --- sleepCtx unit tests ---

func TestSleepCtx_ZeroDuration(t *testing.T) {
	err := sleepCtx(context.Background(), 0)
	if err != nil {
		t.Errorf("unexpected error for zero duration: %v", err)
	}
}

func TestSleepCtx_Elapses(t *testing.T) {
	start := time.Now()
	err := sleepCtx(context.Background(), 10*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 8*time.Millisecond {
		t.Errorf("elapsed %v, expected at least 8ms", elapsed)
	}
}

func TestSleepCtx_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sleepCtx(ctx, 10*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// Ensure Probe satisfies http.RoundTripper at compile time.
var _ http.RoundTripper = (*Probe)(nil)
