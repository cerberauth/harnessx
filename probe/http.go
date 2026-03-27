package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

const (
	defaultUserAgent  = "harnessx-probe/1.0"
	defaultMaxRetries = 3
	defaultRetryDelay = 500 * time.Millisecond
	defaultTimeout    = 30 * time.Second
)

// ErrMaxRetriesExceeded is returned when all retry attempts have been exhausted.
var ErrMaxRetriesExceeded = errors.New("probe: max retries exceeded")

// Option configures a Probe.
type Option func(*config)

type config struct {
	userAgent  string
	maxRetries int
	retryDelay time.Duration
	transport  http.RoundTripper
	timeout    time.Duration
}

func defaultConfig() config {
	return config{
		userAgent:  defaultUserAgent,
		maxRetries: defaultMaxRetries,
		retryDelay: defaultRetryDelay,
		timeout:    defaultTimeout,
	}
}

// WithUserAgent sets the User-Agent header sent on every request.
func WithUserAgent(ua string) Option {
	return func(c *config) {
		if ua != "" {
			c.userAgent = ua
		}
	}
}

// WithMaxRetries sets the maximum number of retry attempts (0 = no retries, one attempt only).
func WithMaxRetries(n int) Option {
	return func(c *config) {
		if n >= 0 {
			c.maxRetries = n
		}
	}
}

// WithRetryDelay sets the base delay between retries. The actual delay grows
// exponentially with full jitter, capped at 60 seconds.
func WithRetryDelay(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.retryDelay = d
		}
	}
}

// WithTransport sets the underlying http.RoundTripper used for actual requests.
// Defaults to http.DefaultTransport.
func WithTransport(t http.RoundTripper) Option {
	return func(c *config) {
		if t != nil {
			c.transport = t
		}
	}
}

// WithTimeout sets the timeout on the *http.Client returned by Client().
// It has no effect on the underlying transport.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// Probe implements http.RoundTripper with retry, rate-limit compliance, and
// User-Agent injection. Use Client() to get a standard *http.Client backed by
// this probe, giving access to the full net/http API with no verb restrictions.
type Probe struct {
	cfg       config
	transport http.RoundTripper
}

// New creates a Probe with the given options.
func New(opts ...Option) *Probe {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	t := cfg.transport
	if t == nil {
		t = http.DefaultTransport
	}
	return &Probe{cfg: cfg, transport: t}
}

// Client returns a *http.Client backed by this probe. All requests made through
// the returned client benefit from retry, rate-limit compliance, and User-Agent
// injection — for any HTTP method.
func (p *Probe) Client() *http.Client {
	return &http.Client{
		Transport: p,
		Timeout:   p.cfg.timeout,
	}
}

// RoundTrip implements http.RoundTripper. It injects the User-Agent header,
// retries on 429/503 and transient network errors, and respects rate-limit
// directives from Retry-After and X-RateLimit-Reset response headers.
func (p *Probe) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	bodyBytes, err := snapshotBody(req)
	if err != nil {
		return nil, err
	}

	var lastErr error
	var lastResp *http.Response

	for attempt := 0; attempt <= p.cfg.maxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		clone := req.Clone(ctx)
		clone.Header.Set("User-Agent", p.cfg.userAgent)
		if bodyBytes != nil {
			clone.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			clone.ContentLength = int64(len(bodyBytes))
		}

		resp, err := p.transport.RoundTrip(clone)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			if attempt == p.cfg.maxRetries {
				break
			}
			if sleepErr := sleepCtx(ctx, backoffDuration(p.cfg.retryDelay, attempt)); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			// Drain and close to allow connection reuse.
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			lastResp = resp
			lastErr = fmt.Errorf("probe: HTTP %d", resp.StatusCode)
			if attempt == p.cfg.maxRetries {
				break
			}
			wait := retryAfterDuration(resp, p.cfg.retryDelay, attempt)
			if sleepErr := sleepCtx(ctx, wait); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}

		return resp, nil
	}

	return lastResp, fmt.Errorf("%w: %w", ErrMaxRetriesExceeded, lastErr)
}

// snapshotBody reads req.Body into a byte slice so it can be replayed on each
// retry attempt. The body is restored on req after reading.
func snapshotBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	data, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("probe: reading request body: %w", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(data))
	return data, nil
}

// retryAfterDuration returns how long to wait before the next retry attempt.
// It parses response headers in priority order:
//  1. Retry-After: <integer seconds>
//  2. Retry-After: <HTTP-date>
//  3. X-RateLimit-Reset: <unix timestamp>
//  4. Exponential backoff with full jitter
func retryAfterDuration(resp *http.Response, base time.Duration, attempt int) time.Duration {
	if h := resp.Header.Get("Retry-After"); h != "" {
		if secs, err := strconv.ParseInt(h, 10, 64); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
		for _, layout := range []string{http.TimeFormat, time.RFC850, time.ANSIC} {
			if t, err := time.Parse(layout, h); err == nil {
				if d := time.Until(t); d > 0 {
					return d
				}
				return 0
			}
		}
	}

	if h := resp.Header.Get("X-RateLimit-Reset"); h != "" {
		if ts, err := strconv.ParseInt(h, 10, 64); err == nil {
			if d := time.Until(time.Unix(ts, 0)); d > 0 {
				return d
			}
			return 0
		}
	}

	return backoffDuration(base, attempt)
}

// backoffDuration returns a random duration in [0, slot) where slot doubles
// each attempt (exponential backoff with full jitter), capped at 60 seconds.
func backoffDuration(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = defaultRetryDelay
	}
	slot := base * (1 << min(attempt, 10))
	const maxSlot = 60 * time.Second
	slot = min(slot, maxSlot)
	return time.Duration(rand.Int63n(int64(slot) + 1))
}

// sleepCtx waits for d to elapse or ctx to be cancelled, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
