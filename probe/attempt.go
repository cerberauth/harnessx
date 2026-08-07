package probe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Attempt is a fully-drained snapshot of an HTTP response, safe to store and
// compare after the underlying response body has been closed.
type Attempt struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Duration   time.Duration
}

// Do sends req via client and returns a captured Attempt. The response body
// is fully read and closed before Do returns.
func Do(ctx context.Context, client *http.Client, req *http.Request) (Attempt, error) {
	req = req.Clone(ctx)

	start := time.Now()
	resp, err := client.Do(req) //nolint:gosec // G704: probing attacker-supplied/target URLs is this package's purpose (DAST scanner)
	if err != nil {
		return Attempt{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Attempt{}, fmt.Errorf("probe: reading response body: %w", err)
	}

	return Attempt{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       body,
		Duration:   time.Since(start),
	}, nil
}
