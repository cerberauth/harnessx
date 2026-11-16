package callback

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ListenerOptions configures a Listener.
type ListenerOptions struct {
	// Addr is the address to bind, such as ":8089" or "127.0.0.1:0".
	Addr string
	// PublicURL is the base URL the target uses to reach Addr, such as
	// "http://scanner.example.com:8089". It defaults to http://<bound address>,
	// which only a target on the same host or network can reach.
	PublicURL string
	// MaxBodySize caps the request body kept on a Hit; DefaultMaxBodySize when
	// zero.
	MaxBodySize int64
}

// Listener is a Collector that runs its own HTTP server.
type Listener struct {
	base    string
	maxBody int64
	srv     *http.Server
	ln      net.Listener
	done    chan struct{}

	mu       sync.Mutex
	tokens   map[string]token
	hits     map[string][]Hit
	handlers map[string]http.Handler
	waiters  map[string]chan struct{}
	closed   bool
}

type token struct{ checkID, resourceID string }

// Listen binds opts.Addr and serves callbacks until ctx is done or Close is
// called.
func Listen(ctx context.Context, opts ListenerOptions) (*Listener, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", opts.Addr)
	if err != nil {
		return nil, err
	}
	l := &Listener{
		base:     strings.TrimRight(opts.PublicURL, "/"),
		maxBody:  opts.MaxBodySize,
		ln:       ln,
		done:     make(chan struct{}),
		tokens:   make(map[string]token),
		hits:     make(map[string][]Hit),
		handlers: make(map[string]http.Handler),
		waiters:  make(map[string]chan struct{}),
	}
	if l.base == "" {
		l.base = "http://" + ln.Addr().String()
	}
	if l.maxBody <= 0 {
		l.maxBody = DefaultMaxBodySize
	}
	l.srv = &http.Server{Handler: l, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		defer close(l.done)
		_ = l.srv.Serve(ln)
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = l.shutdown(context.WithoutCancel(ctx))
		case <-l.done:
		}
	}()
	return l, nil
}

// Addr returns the bound address.
func (l *Listener) Addr() net.Addr { return l.ln.Addr() }

// BaseURL returns the base URL tokens are built on.
func (l *Listener) BaseURL() string { return l.base }

// NewToken implements Collector.
func (l *Listener) NewToken(checkID, resourceID string) (string, string) {
	id := newID()
	l.mu.Lock()
	l.tokens[id] = token{checkID: checkID, resourceID: resourceID}
	l.mu.Unlock()
	return l.base + "/" + id, id
}

// Received implements Collector.
func (l *Listener) Received(_ context.Context, id string) (Hit, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if hits := l.hits[id]; len(hits) > 0 {
		return hits[0], true
	}
	return Hit{}, false
}

// Hits returns every request made to the URL of id.
func (l *Listener) Hits(id string) []Hit {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Hit(nil), l.hits[id]...)
}

// Handle implements Collector.
func (l *Listener) Handle(id string, h http.Handler) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.tokens[id]; !ok {
		return errors.New("callback: unknown token")
	}
	l.handlers[id] = h
	return nil
}

// Close stops the server. It is safe to call more than once.
func (l *Listener) Close() error {
	return l.shutdown(context.Background())
}

func (l *Listener) shutdown(ctx context.Context) error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		<-l.done
		return nil
	}
	l.closed = true
	l.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := l.srv.Shutdown(ctx)
	<-l.done
	return err
}

// Done is closed once the server has stopped.
func (l *Listener) Done() <-chan struct{} { return l.done }

func (l *Listener) notify(id string) <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits[id]) > 0 {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	ch, ok := l.waiters[id]
	if !ok {
		ch = make(chan struct{})
		l.waiters[id] = ch
	}
	return ch
}

// ServeHTTP records a request to a token URL and serves its handler.
func (l *Listener) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")

	l.mu.Lock()
	tok, known := l.tokens[id]
	l.mu.Unlock()
	if !known {
		http.NotFound(w, r)
		return
	}

	body, truncated := readCapped(r.Body, l.maxBody)
	hit := Hit{
		ID:            id,
		CheckID:       tok.checkID,
		ResourceID:    tok.resourceID,
		Time:          time.Now(),
		RemoteAddr:    r.RemoteAddr,
		Method:        r.Method,
		Path:          r.URL.RequestURI(),
		Header:        r.Header.Clone(),
		Body:          body,
		BodyTruncated: truncated,
	}

	l.mu.Lock()
	l.hits[id] = append(l.hits[id], hit)
	if ch, ok := l.waiters[id]; ok {
		close(ch)
		delete(l.waiters, id)
	}
	h := l.handlers[id]
	l.mu.Unlock()

	if h != nil {
		h.ServeHTTP(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func readCapped(body io.Reader, limit int64) ([]byte, bool) {
	if body == nil {
		return nil, false
	}
	data, _ := io.ReadAll(io.LimitReader(body, limit+1))
	_, _ = io.Copy(io.Discard, body)
	if int64(len(data)) > limit {
		return data[:limit], true
	}
	return data, false
}
