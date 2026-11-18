// Package callback confirms out-of-band interactions: a check hands the target
// a unique URL and later asks whether the target requested it.
package callback

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// DefaultMaxBodySize caps the request body kept on a Hit.
const DefaultMaxBodySize int64 = 64 << 10

// ErrHandleUnsupported is returned by Handle when the collector cannot serve
// content, such as a Poller.
var ErrHandleUnsupported = errors.New("callback: collector cannot serve content")

// Hit is one request a target made to a callback URL.
type Hit struct {
	ID         string
	CheckID    string
	ResourceID string
	Time       time.Time
	RemoteAddr string
	Method     string
	Path       string
	Header     http.Header
	// Body holds at most the collector's body cap; BodyTruncated reports
	// whether more was sent.
	Body          []byte
	BodyTruncated bool
}

// String describes the hit for report evidence.
func (h Hit) String() string {
	return fmt.Sprintf("%s %s from %s at %s", h.Method, h.Path, h.RemoteAddr, h.Time.UTC().Format(time.RFC3339))
}

// Collector hands out callback URLs and reports the requests made to them.
type Collector interface {
	// NewToken returns a unique URL for one probe of checkID on resourceID,
	// and the ID to look it up with.
	NewToken(checkID, resourceID string) (url, id string)
	// Received reports the first request made to the URL of id.
	Received(ctx context.Context, id string) (Hit, bool)
	// Handle sets the content served for requests to the URL of id.
	Handle(id string, h http.Handler) error
	// Close releases the collector.
	Close() error
}

// DefaultPollInterval is how often WaitReceived asks a collector that cannot
// notify it.
const DefaultPollInterval = 250 * time.Millisecond

type notifier interface {
	notify(id string) <-chan struct{}
}

// WaitReceived waits up to timeout for a request to the URL of id, returning
// as soon as one arrives.
func WaitReceived(ctx context.Context, c Collector, id string, timeout time.Duration) (Hit, bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if hit, ok := c.Received(ctx, id); ok {
		return hit, true
	}
	if n, ok := c.(notifier); ok {
		select {
		case <-n.notify(id):
			return c.Received(context.WithoutCancel(ctx), id)
		case <-ctx.Done():
			return Hit{}, false
		}
	}

	interval := DefaultPollInterval
	if p, ok := c.(*Poller); ok && p.interval > 0 {
		interval = p.interval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Hit{}, false
		case <-ticker.C:
			if hit, ok := c.Received(ctx, id); ok {
				return hit, true
			}
		}
	}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
