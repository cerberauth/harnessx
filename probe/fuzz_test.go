package probe

import (
	"net/http"
	"testing"
	"time"
)

func FuzzRetryAfterDuration(f *testing.F) {
	for _, s := range []string{"", "0", "-1", "30", "86400", "9223372036854775807", "Wed, 21 Oct 2099 07:28:00 GMT", "garbage"} {
		f.Add(s, s, 3)
	}
	f.Add("1", "", -1)
	f.Fuzz(func(t *testing.T, retryAfter, reset string, attempt int) {
		resp := &http.Response{Header: http.Header{}}
		resp.Header.Set("Retry-After", retryAfter)
		resp.Header.Set("X-RateLimit-Reset", reset)
		if d := retryAfterDuration(resp, 10*time.Millisecond, attempt); d < 0 {
			t.Errorf("negative duration %v for Retry-After=%q X-RateLimit-Reset=%q attempt=%d", d, retryAfter, reset, attempt)
		}
	})
}
