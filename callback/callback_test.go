package callback_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cerberauth/harnessx/callback"
)

func listen(t *testing.T, opts callback.ListenerOptions) *callback.Listener {
	t.Helper()
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:0"
	}
	l, err := callback.Listen(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func fetch(t *testing.T, method, u string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, u, body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestListenerConfirmsFetch(t *testing.T) {
	l := listen(t, callback.ListenerOptions{})

	u, id := l.NewToken("jwt.jku_injection", "op-1")
	go func() {
		time.Sleep(20 * time.Millisecond)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, u+"/jwks.json?x=1", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()

	hit, ok := callback.WaitReceived(context.Background(), l, id, 2*time.Second)
	if !ok {
		t.Fatal("hit not received")
	}
	if hit.ID != id || hit.CheckID != "jwt.jku_injection" || hit.ResourceID != "op-1" || hit.Method != http.MethodGet || hit.Path != "/"+id+"/jwks.json?x=1" || hit.RemoteAddr == "" || hit.Time.IsZero() {
		t.Errorf("hit = %+v", hit)
	}
	if !strings.Contains(hit.String(), "GET /"+id) {
		t.Errorf("String() = %q", hit.String())
	}
}

func TestListenerNotConfirmedWhenIgnored(t *testing.T) {
	l := listen(t, callback.ListenerOptions{})
	_, id := l.NewToken("ssrf", "")
	start := time.Now()
	if _, ok := callback.WaitReceived(context.Background(), l, id, 100*time.Millisecond); ok {
		t.Fatal("unexpected hit")
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Error("WaitReceived returned before the timeout")
	}
}

func TestListenerCorrelatesConcurrentProbes(t *testing.T) {
	l := listen(t, callback.ListenerOptions{})
	const n = 20
	urls := make([]string, n)
	ids := make([]string, n)
	seen := map[string]bool{}
	for i := range n {
		urls[i], ids[i] = l.NewToken("check", string(rune('a'+i)))
		if seen[ids[i]] {
			t.Fatalf("duplicate id %q", ids[i])
		}
		seen[ids[i]] = true
	}

	var wg sync.WaitGroup
	for i := range n {
		if i%2 == 1 {
			continue
		}
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, u, strings.NewReader(u))
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}(urls[i])
	}
	wg.Wait()

	for i := range n {
		hit, ok := l.Received(context.Background(), ids[i])
		if ok != (i%2 == 0) {
			t.Fatalf("probe %d: received = %v", i, ok)
		}
		if ok && (string(hit.Body) != urls[i] || hit.ResourceID != string(rune('a'+i))) {
			t.Errorf("probe %d: hit = %+v", i, hit)
		}
	}
}

func TestListenerServesHandlerAndCapsBody(t *testing.T) {
	l := listen(t, callback.ListenerOptions{MaxBodySize: 4})
	u, id := l.NewToken("check", "")
	if err := l.Handle(id, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"keys":[]}`))
	})); err != nil {
		t.Fatal(err)
	}
	if err := l.Handle("unknown", http.NotFoundHandler()); err == nil {
		t.Error("expected an error for an unknown token")
	}

	resp := fetch(t, http.MethodPost, u, strings.NewReader("0123456789"))
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"keys":[]}` {
		t.Errorf("body = %q", body)
	}
	hit, ok := l.Received(context.Background(), id)
	if !ok || string(hit.Body) != "0123" || !hit.BodyTruncated {
		t.Errorf("hit = %+v", hit)
	}

	if resp := fetch(t, http.MethodGet, l.BaseURL()+"/not-a-token", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown token status = %d", resp.StatusCode)
	}
	if len(l.Hits("not-a-token")) != 0 {
		t.Error("unknown token recorded")
	}
}

func TestListenerPublicURL(t *testing.T) {
	l := listen(t, callback.ListenerOptions{PublicURL: "https://oob.example.com/"})
	u, id := l.NewToken("check", "")
	if u != "https://oob.example.com/"+id {
		t.Errorf("url = %q", u)
	}
}

func TestListenerShutsDownOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l, err := callback.Listen(ctx, callback.ListenerOptions{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	cancel()

	select {
	case <-l.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("listener still running after cancel")
	}
	var d net.Dialer
	if conn, err := d.DialContext(context.Background(), "tcp", addr); err == nil {
		_ = conn.Close()
		t.Error("listener still accepting connections")
	}
	if err := l.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
}

type collectorStub struct {
	mu   sync.Mutex
	hits map[string][]map[string]any
}

func (c *collectorStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/hits" {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		c.mu.Lock()
		hits := c.hits[r.URL.Query().Get("id")]
		c.mu.Unlock()
		if hits == nil {
			hits = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(hits)
		return
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	c.mu.Lock()
	c.hits[id] = append(c.hits[id], map[string]any{
		"time": time.Now().UTC(), "remote_addr": r.RemoteAddr, "method": r.Method, "path": r.URL.Path,
	})
	c.mu.Unlock()
}

func TestPoller(t *testing.T) {
	stub := &collectorStub{hits: map[string][]map[string]any{}}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	p := callback.NewPoller(callback.PollerOptions{
		BaseURL:  srv.URL,
		Header:   http.Header{"Authorization": {"Bearer secret"}},
		Interval: 10 * time.Millisecond,
	})
	defer p.Close()

	u, id := p.NewToken("ssrf", "")
	_, other := p.NewToken("ssrf", "")
	if id == other {
		t.Fatal("ids not unique")
	}
	if !errors.Is(p.Handle(id, http.NotFoundHandler()), callback.ErrHandleUnsupported) {
		t.Error("Handle should be unsupported")
	}

	go func() {
		time.Sleep(30 * time.Millisecond)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, u+"/x", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	hit, ok := callback.WaitReceived(context.Background(), p, id, 2*time.Second)
	if !ok || hit.Path != "/"+id+"/x" || hit.Method != http.MethodGet {
		t.Fatalf("hit = %+v, %v", hit, ok)
	}
	if _, ok := callback.WaitReceived(context.Background(), p, other, 50*time.Millisecond); ok {
		t.Error("unexpected hit for an unused token")
	}

	bad := callback.NewPoller(callback.PollerOptions{BaseURL: srv.URL})
	if _, err := bad.Hits(context.Background(), id); err == nil {
		t.Error("expected an error for an unauthorized poll")
	}
}
