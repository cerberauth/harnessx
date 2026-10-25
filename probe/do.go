package probe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultMaxBodySize is the response body cap applied by Do and by DoResponse
// when no WithMaxBodySize option is given.
const DefaultMaxBodySize = 10 << 20 // 10 MiB

// maxDrainSize bounds how much of an over-limit body is discarded before the
// connection is closed, so a hostile endless stream cannot stall the caller.
const maxDrainSize = 64 << 10

// Response is a fully-read HTTP response captured by DoResponse.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Duration   time.Duration

	// Truncated reports that the body was longer than the configured cap and
	// Body holds only its first bytes.
	Truncated bool
}

// DoOption configures DoResponse.
type DoOption func(*doConfig)

type doConfig struct {
	maxBodySize int64
}

// WithMaxBodySize caps how many response body bytes are kept in memory. Bytes
// beyond n are discarded and Response.Truncated is set. n <= 0 means unlimited.
func WithMaxBodySize(n int64) DoOption {
	return func(c *doConfig) {
		c.maxBodySize = n
	}
}

// Do sends req via client and returns the response's status code, headers,
// fully-drained body, and duration. The response body is read, up to
// DefaultMaxBodySize bytes, and closed before Do returns; anything beyond the
// cap is dropped silently. Use DoResponse to detect truncation or change the cap.
func Do(ctx context.Context, client *http.Client, req *http.Request) (statusCode int, header http.Header, body []byte, duration time.Duration, err error) {
	resp, err := DoResponse(ctx, client, req)
	if err != nil {
		return 0, nil, nil, 0, err
	}
	return resp.StatusCode, resp.Header, resp.Body, resp.Duration, nil
}

// DoResponse sends req via client and returns the captured Response. The body
// is capped at DefaultMaxBodySize bytes unless WithMaxBodySize says otherwise;
// when the server sends more, Response.Truncated is set. The response body is
// closed before DoResponse returns.
func DoResponse(ctx context.Context, client *http.Client, req *http.Request, opts ...DoOption) (Response, error) {
	cfg := doConfig{maxBodySize: DefaultMaxBodySize}
	for _, o := range opts {
		o(&cfg)
	}

	req = req.Clone(ctx)

	start := time.Now()
	resp, err := client.Do(req) //nolint:gosec // G704: probing attacker-supplied/target URLs is this package's purpose (DAST scanner)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	var reader io.Reader = resp.Body
	if cfg.maxBodySize > 0 {
		reader = io.LimitReader(resp.Body, cfg.maxBodySize+1)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return Response{}, fmt.Errorf("probe: reading response body: %w", err)
	}

	truncated := cfg.maxBodySize > 0 && int64(len(body)) > cfg.maxBodySize
	if truncated {
		body = body[:cfg.maxBodySize]
		// Best-effort drain so the connection can be reused when the excess is small.
		_, _ = io.CopyN(io.Discard, resp.Body, maxDrainSize)
	}

	return Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       body,
		Duration:   time.Since(start),
		Truncated:  truncated,
	}, nil
}
