package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
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
	lim := rate.NewLimiter(rate.Limit(1), 1)
	p := New(
		WithUserAgent("my-agent/2.0"),
		WithMaxRetries(5),
		WithRetryDelay(100*time.Millisecond),
		WithTransport(customTransport),
		WithTimeout(10*time.Second),
		WithRetryNonIdempotent(),
		WithRateLimiter(lim),
	)
	if p.cfg.limiter != lim {
		t.Error("limiter not set")
	}
	if !p.cfg.retryNonIdempotent {
		t.Error("retryNonIdempotent not set")
	}
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
	_ = resp.Body.Close()
	if gotUA != "test-agent/1.0" {
		t.Errorf("User-Agent = %q, want test-agent/1.0", gotUA)
	}
}

func TestRoundTrip_UserAgent_CallerValueWins(t *testing.T) {
	const callerUA = "vulnapi/1.2"
	var gotUA []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Values("User-Agent")
	}))
	defer srv.Close()

	// Retries must keep the caller's value too, not only the first attempt.
	var calls atomic.Int32
	flaky := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Values("User-Agent")
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
		}
	})
	retrySrv := httptest.NewServer(flaky)
	defer retrySrv.Close()

	for name, url := range map[string]string{"single attempt": srv.URL, "after retry": retrySrv.URL} {
		t.Run(name, func(t *testing.T) {
			gotUA = nil
			p := New(WithUserAgent("probe-default/1.0"), WithRetryDelay(1*time.Millisecond))
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("User-Agent", callerUA)
			resp, err := p.Client().Do(req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			_ = resp.Body.Close()
			if len(gotUA) != 1 || gotUA[0] != callerUA {
				t.Errorf("User-Agent = %q, want only %q", gotUA, callerUA)
			}
		})
	}
}

func TestRoundTrip_UserAgent_DefaultAppliedWhenAbsent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	p := New()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := p.Client().Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()
	if gotUA != defaultUserAgent {
		t.Errorf("User-Agent = %q, want %q", gotUA, defaultUserAgent)
	}
}

func TestRoundTrip_UserAgent_DoesNotMutateCallerRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := New().RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()
	if got := req.Header.Get("User-Agent"); got != "" {
		t.Errorf("caller request User-Agent = %q, want it untouched", got)
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
		_ = resp.Body.Close()
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
	_ = resp.Body.Close()
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
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status = %d", resp.StatusCode)
	}
}

func TestRoundTrip_RetriesExhausted_ReturnsFinalResponse(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprintf("%d", status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "slow down")
			}))
			defer srv.Close()

			p := New(WithMaxRetries(2), WithRetryDelay(1*time.Millisecond))
			req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
			resp, err := p.RoundTrip(req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != status {
				t.Errorf("status = %d, want %d", resp.StatusCode, status)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading body: %v", err)
			}
			if string(body) != "slow down" {
				t.Errorf("body = %q, want %q", body, "slow down")
			}
			// maxRetries=2 → 3 total attempts
			if calls.Load() != 3 {
				t.Errorf("calls = %d, want 3", calls.Load())
			}
		})
	}
}

func TestRoundTrip_NeverReturnsResponseAndError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := New(WithMaxRetries(1), WithRetryDelay(1*time.Millisecond))
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := p.RoundTrip(req)
	if resp != nil && err != nil {
		t.Fatalf("got both response (%d) and error (%v)", resp.StatusCode, err)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}

	srv.Close()
	resp, err = p.RoundTrip(req.Clone(context.Background()))
	if resp != nil && err != nil {
		t.Fatalf("got both response and error: %v", err)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrMaxRetriesExceeded) {
		t.Errorf("error = %v, want ErrMaxRetriesExceeded", err)
	}
}

func TestRoundTrip_ClosedListener_ErrMaxRetriesExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	p := New(WithMaxRetries(1), WithRetryDelay(1*time.Millisecond))
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	resp, err := p.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
		t.Fatalf("response = %v, want nil", resp.StatusCode)
	}
	if !errors.Is(err, ErrMaxRetriesExceeded) {
		t.Errorf("error = %v, want ErrMaxRetriesExceeded", err)
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
			_ = resp.Body.Close()
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
			conn, _, err := hj.Hijack()
			if err != nil {
				return
			}
			_ = conn.Close()
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
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestRoundTrip_NetworkError_ExhaustedWrapsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
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
	_ = resp.Body.Close()

	for i, body := range received {
		if body != wantBody {
			t.Errorf("attempt %d body = %q, want %q", i, body, wantBody)
		}
	}
}

func TestRoundTrip_NonIdempotent(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		status     int
		header     string
		opts       []Option
		wantHits   int32
		wantStatus int
	}{
		{"GET 503 retried", http.MethodGet, 503, "", nil, 3, 503},
		{"PUT 503 retried", http.MethodPut, 503, "", nil, 3, 503},
		{"DELETE 503 retried", http.MethodDelete, 503, "", nil, 3, 503},
		{"POST 503 not retried", http.MethodPost, 503, "", nil, 1, 503},
		{"PATCH 503 not retried", http.MethodPatch, 503, "", nil, 1, 503},
		{"POST 429 retried", http.MethodPost, 429, "", nil, 3, 429},
		{"PATCH 429 retried", http.MethodPatch, 429, "", nil, 3, 429},
		{"POST 503 with opt-in retried", http.MethodPost, 503, "", []Option{WithRetryNonIdempotent()}, 3, 503},
		{"POST 503 with Idempotency-Key retried", http.MethodPost, 503, "idem-key-1", nil, 3, 503},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			opts := append([]Option{WithMaxRetries(2), WithRetryDelay(1 * time.Millisecond)}, tt.opts...)
			p := New(opts...)
			req, _ := http.NewRequest(tt.method, srv.URL, strings.NewReader("x"))
			if tt.header != "" {
				req.Header.Set("Idempotency-Key", tt.header)
			}
			resp, err := p.RoundTrip(req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if hits.Load() != tt.wantHits {
				t.Errorf("hits = %d, want %d", hits.Load(), tt.wantHits)
			}
		})
	}
}

func TestRoundTrip_NonIdempotent_NetworkError(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		opts     []Option
		wantHits int32
	}{
		{"GET retried", http.MethodGet, nil, 2},
		{"POST not retried", http.MethodPost, nil, 1},
		{"POST with opt-in retried", http.MethodPost, []Option{WithRetryNonIdempotent()}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}))
			defer srv.Close()

			opts := append([]Option{WithMaxRetries(1), WithRetryDelay(1 * time.Millisecond)}, tt.opts...)
			p := New(opts...)
			req, _ := http.NewRequest(tt.method, srv.URL, strings.NewReader("x"))
			resp, err := p.RoundTrip(req)
			if resp != nil {
				_ = resp.Body.Close()
				t.Fatalf("response = %d, want nil", resp.StatusCode)
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if hits.Load() != tt.wantHits {
				t.Errorf("hits = %d, want %d", hits.Load(), tt.wantHits)
			}
		})
	}
}

func TestIsIdempotent(t *testing.T) {
	for method, want := range map[string]bool{
		"": true, http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true,
		http.MethodTrace: true, http.MethodPut: true, http.MethodDelete: true,
		http.MethodPost: false, http.MethodPatch: false, http.MethodConnect: false,
	} {
		req, _ := http.NewRequest(method, "http://example.com", nil)
		if got := isIdempotent(req); got != want {
			t.Errorf("%q: isIdempotent = %v, want %v", method, got, want)
		}
	}
}

func TestRoundTrip_RateLimiter_Paces(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	p := New(WithRateLimiter(rate.NewLimiter(rate.Every(100*time.Millisecond), 1)))
	start := time.Now()
	for range 5 {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		resp, err := p.RoundTrip(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_ = resp.Body.Close()
	}
	// First request uses the burst token; the other four wait 100ms each.
	if elapsed := time.Since(start); elapsed < 390*time.Millisecond {
		t.Errorf("5 requests took %v, want >= ~400ms", elapsed)
	}
	if hits.Load() != 5 {
		t.Errorf("hits = %d, want 5", hits.Load())
	}
}

func TestRoundTrip_RateLimiter_RetriesConsumeTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	// Burst 1, no refill within the test: the second attempt must block
	// until the context expires.
	lim := rate.NewLimiter(rate.Every(time.Hour), 1)
	p := New(WithMaxRetries(3), WithRetryDelay(1*time.Millisecond), WithRateLimiter(lim))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := p.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
		t.Fatalf("response = %d, want nil", resp.StatusCode)
	}
	if err == nil {
		t.Fatal("expected error from blocked retry, got nil")
	}
	if lim.Tokens() >= 1 {
		t.Errorf("tokens = %v, want the retry to have consumed the burst", lim.Tokens())
	}
}

func TestRoundTrip_RateLimiter_ContextCancelUnblocks(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	lim := rate.NewLimiter(rate.Every(time.Hour), 1)
	p := New(WithRateLimiter(lim))

	// Drain the burst token.
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := p.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)

	start := time.Now()
	_, err = p.RoundTrip(req)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waiter took %v to unblock, want prompt", elapsed)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1 (blocked request must not be sent)", hits.Load())
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
	data, hasBody, err := snapshotBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasBody || data != nil {
		t.Errorf("data = %v, hasBody = %v, want no body", data, hasBody)
	}
}

func TestSnapshotBody_UsesGetBodyAndLeavesRequestAlone(t *testing.T) {
	const want = "body content"
	req, _ := http.NewRequest(http.MethodPost, "http://example.com", strings.NewReader(want))
	orig := req.Body
	data, hasBody, err := snapshotBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasBody || string(data) != want {
		t.Errorf("data = %q, hasBody = %v, want %q", data, hasBody, want)
	}
	if req.Body != orig {
		t.Error("snapshotBody replaced req.Body")
	}
}

func TestSnapshotBody_ReadsBodyWithoutGetBody(t *testing.T) {
	const want = "body content"
	req, _ := http.NewRequest(http.MethodPost, "http://example.com", io.NopCloser(strings.NewReader(want)))
	if req.GetBody != nil {
		t.Fatal("test needs a request without GetBody")
	}
	orig := req.Body
	data, hasBody, err := snapshotBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasBody || string(data) != want {
		t.Errorf("data = %q, hasBody = %v, want %q", data, hasBody, want)
	}
	if req.Body != orig {
		t.Error("snapshotBody replaced req.Body")
	}
}

func TestSnapshotBody_EmptyBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "http://example.com", io.NopCloser(strings.NewReader("")))
	data, hasBody, err := snapshotBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasBody || len(data) != 0 {
		t.Errorf("data = %q, hasBody = %v, want an empty body", data, hasBody)
	}
}

func TestSnapshotBody_NoBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", http.NoBody)
	data, hasBody, err := snapshotBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasBody || data != nil {
		t.Errorf("data = %v, hasBody = %v, want no body for http.NoBody", data, hasBody)
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

// --- RoundTripper contract and request-body handling ---

func TestRoundTrip_DoesNotModifyRequest(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
	}))
	defer srv.Close()

	body := io.NopCloser(strings.NewReader("payload"))
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, body)
	req.ContentLength = -1

	resp, err := New(WithMaxRetries(0)).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	if got != "payload" {
		t.Errorf("server body = %q, want %q", got, "payload")
	}
	if req.Body != body {
		t.Error("RoundTrip replaced the caller's req.Body")
	}
	if req.ContentLength != -1 {
		t.Errorf("RoundTrip changed req.ContentLength to %d", req.ContentLength)
	}
	if req.Header.Get("User-Agent") != "" {
		t.Error("RoundTrip set a header on the caller's request")
	}
}

func TestRoundTrip_EmptyBodyHasKnownLength(t *testing.T) {
	var contentLength int64
	var transferEncoding []string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		contentLength = r.ContentLength
		transferEncoding = r.TransferEncoding
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, io.NopCloser(strings.NewReader("")))
	resp, err := New(WithMaxRetries(0)).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	if contentLength != 0 {
		t.Errorf("ContentLength = %d, want 0", contentLength)
	}
	if len(transferEncoding) != 0 {
		t.Errorf("TransferEncoding = %v, want none (not chunked)", transferEncoding)
	}
}

func TestRetryAfterDuration_RateLimitResetDelta(t *testing.T) {
	tests := []struct {
		name   string
		reset  string
		want   time.Duration
		approx bool
	}{
		{name: "delta seconds", reset: "30", want: 30 * time.Second},
		{name: "zero", reset: "0", want: 0},
		{name: "negative", reset: "-5", want: 0},
		{name: "future epoch", reset: strconv.FormatInt(time.Now().Add(20*time.Second).Unix(), 10), want: 20 * time.Second, approx: true},
		{name: "past epoch", reset: strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10), want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{"X-Ratelimit-Reset": []string{tt.reset}}}
			got := retryAfterDuration(resp, time.Millisecond, 0)
			if tt.approx {
				if got < tt.want-2*time.Second || got > tt.want {
					t.Errorf("got %v, want about %v", got, tt.want)
				}
				return
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
