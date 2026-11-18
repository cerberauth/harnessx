package callback

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PollerOptions configures a Poller.
type PollerOptions struct {
	// BaseURL is the base URL of the collector the target reaches; tokens are
	// <BaseURL>/<id>.
	BaseURL string
	// PollURL is where hits are read, <PollURL>?id=<id>. It defaults to
	// <BaseURL>/hits.
	PollURL string
	// Header is sent on every poll, for example an Authorization header.
	Header http.Header
	// Client sends the polls; http.DefaultClient when nil.
	Client *http.Client
	// Interval is how often WaitReceived polls; DefaultPollInterval when zero.
	Interval time.Duration
}

// Poller is a Collector backed by a collector the user runs. It cannot serve
// content, so Handle returns ErrHandleUnsupported.
//
// Polling contract: the collector records every request whose path starts
// with /<id> under BaseURL, and answers GET <PollURL>?id=<id> with 200 and a
// JSON array of hits, oldest first, empty when none:
//
//	[{"time": "2026-01-02T15:04:05Z", "remote_addr": "203.0.113.7:51234",
//	  "method": "GET", "path": "/<id>/jwks.json", "headers": {"User-Agent": ["..."]},
//	  "body": "...", "body_truncated": false}]
//
// Only time, remote_addr, method and path are required.
type Poller struct {
	base     string
	poll     string
	header   http.Header
	client   *http.Client
	interval time.Duration
}

// NewPoller returns a Poller for opts.
func NewPoller(opts PollerOptions) *Poller {
	p := &Poller{
		base:     strings.TrimRight(opts.BaseURL, "/"),
		poll:     opts.PollURL,
		header:   opts.Header,
		client:   opts.Client,
		interval: opts.Interval,
	}
	if p.poll == "" {
		p.poll = p.base + "/hits"
	}
	if p.client == nil {
		p.client = http.DefaultClient
	}
	return p
}

// NewToken implements Collector. checkID and resourceID are not sent to the
// collector.
func (p *Poller) NewToken(_, _ string) (string, string) {
	id := newID()
	return p.base + "/" + id, id
}

type wireHit struct {
	Time          time.Time   `json:"time"`
	RemoteAddr    string      `json:"remote_addr"`
	Method        string      `json:"method"`
	Path          string      `json:"path"`
	Headers       http.Header `json:"headers"`
	Body          string      `json:"body"`
	BodyTruncated bool        `json:"body_truncated"`
}

// Received implements Collector. A failed poll reports no hit.
func (p *Poller) Received(ctx context.Context, id string) (Hit, bool) {
	hits, err := p.Hits(ctx, id)
	if err != nil || len(hits) == 0 {
		return Hit{}, false
	}
	return hits[0], true
}

// Hits polls the collector for every request made to the URL of id.
func (p *Poller) Hits(ctx context.Context, id string) ([]Hit, error) {
	u, err := url.Parse(p.poll)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("id", id)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	for k, v := range p.header {
		req.Header[k] = v
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("callback: poll returned %s", resp.Status)
	}

	var wire []wireHit
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&wire); err != nil {
		return nil, fmt.Errorf("callback: decoding poll response: %w", err)
	}
	hits := make([]Hit, len(wire))
	for i, w := range wire {
		hits[i] = Hit{
			ID:            id,
			Time:          w.Time,
			RemoteAddr:    w.RemoteAddr,
			Method:        w.Method,
			Path:          w.Path,
			Header:        w.Headers,
			Body:          []byte(w.Body),
			BodyTruncated: w.BodyTruncated,
		}
	}
	return hits, nil
}

// Handle implements Collector; a Poller cannot serve content.
func (p *Poller) Handle(string, http.Handler) error { return ErrHandleUnsupported }

// Close implements Collector.
func (p *Poller) Close() error { return nil }
