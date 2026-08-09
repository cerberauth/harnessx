package probe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Do sends req via client and returns the response's status code, headers,
// fully-drained body, and duration. The response body is fully read and
// closed before Do returns.
func Do(ctx context.Context, client *http.Client, req *http.Request) (statusCode int, header http.Header, body []byte, duration time.Duration, err error) {
	req = req.Clone(ctx)

	start := time.Now()
	resp, err := client.Do(req) //nolint:gosec // G704: probing attacker-supplied/target URLs is this package's purpose (DAST scanner)
	if err != nil {
		return 0, nil, nil, 0, err
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, 0, fmt.Errorf("probe: reading response body: %w", err)
	}

	return resp.StatusCode, resp.Header, body, time.Since(start), nil
}
