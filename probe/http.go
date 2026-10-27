package probe

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"
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

	retryNonIdempotent bool
	limiter            *rate.Limiter
}

func defaultConfig() config {
	return config{
		userAgent:  defaultUserAgent,
		maxRetries: defaultMaxRetries,
		retryDelay: defaultRetryDelay,
		timeout:    defaultTimeout,
	}
}

// WithUserAgent sets the default User-Agent header, sent on every request that
// does not already carry its own. A User-Agent set by the caller on the request
// is never overridden.
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

// WithRetryNonIdempotent allows retrying non-idempotent requests (e.g. POST,
// PATCH) on network errors and 503 responses. By default such requests are
// only retried on 429, since a request that was sent or processed before the
// failure may otherwise be applied twice.
func WithRetryNonIdempotent() Option {
	return func(c *config) {
		c.retryNonIdempotent = true
	}
}

// WithRateLimiter makes the probe wait on l before every attempt, retries
// included. A nil limiter (the default) means no limit.
//
// A limiter is safe for concurrent use, so sharing one across several Probes
// enforces a single global rate over all of them.
func WithRateLimiter(l *rate.Limiter) Option {
	return func(c *config) {
		c.limiter = l
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

// RoundTrip implements http.RoundTripper. It injects the default User-Agent
// header when the request has none (a caller-set User-Agent is kept),
// retries on 429/503 and transient network errors, and respects rate-limit
// directives from Retry-After and X-RateLimit-Reset response headers.
//
// If a rate limiter is configured (see WithRateLimiter), RoundTrip waits for
// a token before every attempt, retries included. A failed wait, such as a
// cancelled context, is returned as the error.
//
// Requests with a non-idempotent method (RFC 9110 §9.2.2) are only retried on
// 429, where the server did not process them. Network errors and 503 responses
// are returned immediately, unless the request carries an Idempotency-Key
// header or WithRetryNonIdempotent is set.
//
// It never returns both a response and an error. When the final attempt still
// answers 429/503, that response is returned unread with a nil error so the
// caller sees the real status and body. When the final attempt fails with a
// network error, the returned error wraps both ErrMaxRetriesExceeded and the
// last network error.
func (p *Probe) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	bodyBytes, hasBody, err := snapshotBody(req)
	if err != nil {
		return nil, err
	}

	retryUnsafe := p.cfg.retryNonIdempotent || isIdempotent(req)

	var lastErr error

	for attempt := 0; attempt <= p.cfg.maxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if p.cfg.limiter != nil {
			if err := p.cfg.limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}

		clone := req.Clone(ctx)
		if clone.Header.Get("User-Agent") == "" {
			clone.Header.Set("User-Agent", p.cfg.userAgent)
		}
		if hasBody {
			setBody(clone, bodyBytes)
		}

		resp, err := p.transport.RoundTrip(clone)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if !retryUnsafe {
				return nil, err
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

		retryable := resp.StatusCode == http.StatusTooManyRequests ||
			(resp.StatusCode == http.StatusServiceUnavailable && retryUnsafe)
		if retryable {
			if attempt == p.cfg.maxRetries {
				// Last attempt: hand the response to the caller unread.
				return resp, nil
			}
			// Drain and close to allow connection reuse.
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			wait := retryAfterDuration(resp, p.cfg.retryDelay, attempt)
			if sleepErr := sleepCtx(ctx, wait); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("%w: %w", ErrMaxRetriesExceeded, lastErr)
}

// isIdempotent reports whether req may be safely sent more than once: its
// method is idempotent per RFC 9110 §9.2.2 or it carries an Idempotency-Key.
func isIdempotent(req *http.Request) bool {
	switch req.Method {
	case "", http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodPut, http.MethodDelete:
		return true
	}
	return req.Header.Get("Idempotency-Key") != ""
}

// snapshotBody reads the request body into a byte slice so it can be replayed
// on each retry attempt, and reports whether the request has a body at all. It
// leaves req untouched, as an http.RoundTripper must: it reads through
// req.GetBody when set, otherwise it consumes req.Body, which RoundTrip would
// have done anyway. req.Body is closed either way.
func snapshotBody(req *http.Request) (data []byte, hasBody bool, err error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, false, nil
	}
	src := req.Body
	if req.GetBody != nil {
		fresh, gerr := req.GetBody()
		if gerr != nil {
			_ = req.Body.Close()
			return nil, false, fmt.Errorf("probe: cloning request body: %w", gerr)
		}
		_ = req.Body.Close()
		src = fresh
	}
	data, err = io.ReadAll(src)
	_ = src.Close()
	if err != nil {
		return nil, false, fmt.Errorf("probe: reading request body: %w", err)
	}
	return data, true, nil
}

// setBody gives req a replayable body holding data. An empty body is
// http.NoBody with a known zero length, so net/http does not fall back to
// chunked encoding for it.
func setBody(req *http.Request, data []byte) {
	if len(data) == 0 {
		req.Body = http.NoBody
		req.ContentLength = 0
		req.GetBody = func() (io.ReadCloser, error) { return http.NoBody, nil }
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.ContentLength = int64(len(data))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
}

// maxRetryWait caps every wait between retries, whether computed locally or
// requested by the server.
const maxRetryWait = 60 * time.Second

// retryAfterDuration returns how long to wait before the next retry attempt.
// It parses response headers in priority order:
//  1. Retry-After: <integer seconds>
//  2. Retry-After: <HTTP-date>
//  3. X-RateLimit-Reset: <unix timestamp>, or <seconds until reset> when small
//  4. Exponential backoff with full jitter
//
// Server-supplied waits are capped at maxRetryWait so a hostile or buggy
// server cannot stall a check.
func retryAfterDuration(resp *http.Response, base time.Duration, attempt int) time.Duration {
	if h := resp.Header.Get("Retry-After"); h != "" {
		if secs, err := strconv.ParseInt(h, 10, 64); err == nil && secs > 0 {
			return time.Duration(min(secs, int64(maxRetryWait/time.Second))) * time.Second
		}
		if t, err := http.ParseTime(h); err == nil {
			return min(max(time.Until(t), 0), maxRetryWait)
		}
	}

	if h := resp.Header.Get("X-RateLimit-Reset"); h != "" {
		if ts, err := strconv.ParseInt(h, 10, 64); err == nil {
			// APIs send either an epoch timestamp or the seconds until reset;
			// anything below this cutoff (year 2001 as an epoch) is a delta.
			const epochCutoff = 1e9
			if ts < epochCutoff {
				return time.Duration(min(max(ts, 0), int64(maxRetryWait/time.Second))) * time.Second
			}
			return min(max(time.Until(time.Unix(ts, 0)), 0), maxRetryWait)
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
	slot := base * (1 << min(max(attempt, 0), 10))
	slot = min(slot, maxRetryWait)
	n, err := rand.Int(rand.Reader, big.NewInt(int64(slot)+1))
	if err != nil {
		return slot
	}
	return time.Duration(n.Int64())
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
