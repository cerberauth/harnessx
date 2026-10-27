package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"connection refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true},
		{"connection reset", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, true},
		{"broken pipe", &net.OpError{Op: "write", Err: syscall.EPIPE}, true},
		{"EOF", io.EOF, true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"timeout", &net.OpError{Op: "read", Err: timeoutErr{}}, true},
		{"temporary DNS", &net.DNSError{Err: "try again", IsTemporary: true}, true},
		{"DNS timeout", &net.DNSError{Err: "i/o timeout", IsTimeout: true}, true},
		{"unknown host", &net.DNSError{Err: "no such host", IsNotFound: true}, false},
		{"bad certificate", &tls.CertificateVerificationError{Err: errors.New("x509: unknown authority")}, false},
		{"unsupported scheme", errors.New(`unsupported protocol scheme "gopher"`), false},
	}
	for _, tt := range tests {
		if got := isTransientError(tt.err); got != tt.want {
			t.Errorf("%s: isTransientError = %v, want %v", tt.name, got, tt.want)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestRoundTrip_PermanentErrorIsNotRetried(t *testing.T) {
	var calls atomic.Int64
	p := New(WithMaxRetries(3), WithRetryDelay(time.Millisecond), WithTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, &net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true}
	})))

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://nope.invalid", nil)
	_, err := p.RoundTrip(req)
	if err == nil {
		t.Fatal("want an error")
	}
	if errors.Is(err, ErrMaxRetriesExceeded) {
		t.Errorf("error = %v, want the permanent error itself, not ErrMaxRetriesExceeded", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("transport called %d times, want 1", got)
	}
}

func TestRoundTrip_TransientErrorIsStillRetried(t *testing.T) {
	var calls atomic.Int64
	p := New(WithMaxRetries(2), WithRetryDelay(time.Millisecond), WithTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	})))

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	_, err := p.RoundTrip(req)
	if !errors.Is(err, ErrMaxRetriesExceeded) {
		t.Fatalf("error = %v, want ErrMaxRetriesExceeded", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("transport called %d times, want 3 (1 + 2 retries)", got)
	}
}

func redirectingServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/end", http.StatusFound)
	})
	mux.HandleFunc("/end", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_RedirectPolicy(t *testing.T) {
	srv := redirectingServer(t)
	get := func(p *Probe) int {
		t.Helper()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/start", nil)
		resp, err := p.Client().Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if got := get(New()); got != http.StatusOK {
		t.Errorf("default: status = %d, want the redirect followed (200)", got)
	}
	if got := get(New(WithNoRedirects())); got != http.StatusFound {
		t.Errorf("WithNoRedirects: status = %d, want 302", got)
	}

	var seen []string
	custom := New(WithCheckRedirect(func(req *http.Request, _ []*http.Request) error {
		seen = append(seen, req.URL.Path)
		return http.ErrUseLastResponse
	}))
	if got := get(custom); got != http.StatusFound || len(seen) != 1 || seen[0] != "/end" {
		t.Errorf("WithCheckRedirect: status = %d, seen = %v; want 302 after consulting the policy for /end", got, seen)
	}
}

func TestWithTimeout_ZeroDisablesAndNegativeIsIgnored(t *testing.T) {
	if got := New().Client().Timeout; got != defaultTimeout {
		t.Errorf("default Timeout = %v, want %v", got, defaultTimeout)
	}
	if got := New(WithTimeout(0)).Client().Timeout; got != 0 {
		t.Errorf("WithTimeout(0): Timeout = %v, want 0 (no timeout)", got)
	}
	if got := New(WithTimeout(-time.Second)).Client().Timeout; got != defaultTimeout {
		t.Errorf("WithTimeout(-1s): Timeout = %v, want the default %v", got, defaultTimeout)
	}
	if got := New(WithTimeout(5 * time.Second)).Client().Timeout; got != 5*time.Second {
		t.Errorf("WithTimeout(5s): Timeout = %v", got)
	}
}

func TestProbe_DoUsesClientAndIsNilSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-User-Agent", r.UserAgent())
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := DoResponse(context.Background(), New(WithUserAgent("probe-test")), req)
	if err != nil {
		t.Fatalf("DoResponse via *Probe: %v", err)
	}
	if got := resp.Header.Get("X-Seen-User-Agent"); got != "probe-test" {
		t.Errorf("server saw User-Agent %q, want the probe's", got)
	}

	var nilProbe *Probe
	if _, err := nilProbe.Do(req); err == nil {
		t.Error("nil *Probe.Do: want an error")
	}
	if _, err := DoResponse(context.Background(), nil, req); err == nil {
		t.Error("nil Doer: want an error")
	}
}
