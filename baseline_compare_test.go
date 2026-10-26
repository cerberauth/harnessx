package harnessx

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cerberauth/harnessx/probe"
)

const (
	softNotFoundPage = `<html><head><title>Not Found</title></head><body><h1>Oops</h1>` +
		`<p>The page %s could not be found on this site. Please check the address and try again, ` +
		`or return to the home page to keep browsing our catalogue of products and services.</p></body></html>`
	realPage = `<html><head><title>Admin console</title></head><body><h1>Dashboard</h1>` +
		`<ul><li>Users: 1204</li><li>Active sessions: 87</li><li>Billing</li><li>Audit log</li></ul>` +
		`<form action="/admin/settings" method="post"><input name="csrf" value="x"></form></body></html>`
)

func snap(status int, body string) Snapshot {
	return Snapshot{StatusCode: status, Body: []byte(body)}
}

func TestCompareStatusAndLength(t *testing.T) {
	tests := []struct {
		name      string
		tolerance float64
		base, cur Snapshot
		want      bool
	}{
		{"same", 0.1, snap(200, strings.Repeat("a", 100)), snap(200, strings.Repeat("b", 100)), false},
		{"within tolerance", 0.1, snap(200, strings.Repeat("a", 100)), snap(200, strings.Repeat("a", 109)), false},
		{"at tolerance edge", 0.1, snap(200, strings.Repeat("a", 100)), snap(200, strings.Repeat("a", 110)), false},
		{"above tolerance", 0.1, snap(200, strings.Repeat("a", 100)), snap(200, strings.Repeat("a", 111)), true},
		{"below tolerance", 0.1, snap(200, strings.Repeat("a", 100)), snap(200, strings.Repeat("a", 89)), true},
		{"status differs", 0.5, snap(404, "x"), snap(200, "x"), true},
		{"zero tolerance exact", 0, snap(200, "abc"), snap(200, "abc"), false},
		{"zero tolerance differs", 0, snap(200, "abc"), snap(200, "abcd"), true},
		{"negative tolerance is zero", -1, snap(200, "abc"), snap(200, "abcd"), true},
		{"empty baseline stays empty", 0.5, snap(200, ""), snap(200, ""), false},
		{"empty baseline grows", 0.5, snap(200, ""), snap(200, "x"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := len(CompareStatusAndLength(tt.tolerance)(tt.base, tt.cur)) > 0
			if got != tt.want {
				t.Errorf("deviates = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompareSimilarity(t *testing.T) {
	missing := fmt.Sprintf(softNotFoundPage, "/zq81xk")
	echoed := fmt.Sprintf(softNotFoundPage, "/admin/backup.zip")

	tests := []struct {
		name      string
		threshold float64
		base, cur Snapshot
		want      bool
	}{
		{"identical", 0.9, snap(200, missing), snap(200, missing), false},
		{"soft 404 echoing the path", 0.8, snap(200, missing), snap(200, echoed), false},
		{"different page", 0.8, snap(200, missing), snap(200, realPage), true},
		{"status differs, same body", 0.5, snap(404, missing), snap(200, missing), true},
		{"both empty", 0.9, snap(200, ""), snap(200, ""), false},
		{"empty vs content", 0.9, snap(200, ""), snap(200, realPage), true},
		{"threshold zero never flags body", 0, snap(200, missing), snap(200, realPage), false},
		{"threshold above one flags everything", 1.01, snap(200, missing), snap(200, missing), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := len(CompareSimilarity(tt.threshold)(tt.base, tt.cur)) > 0
			if got != tt.want {
				t.Errorf("deviates = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompareSimilarity_PrefersRecordedBodyHash(t *testing.T) {
	a := Snapshot{StatusCode: 200, BodyHash: SimHash([]byte(realPage))}
	b := Snapshot{StatusCode: 200, Body: []byte(realPage)}
	if obs := CompareSimilarity(0.99)(a, b); len(obs) != 0 {
		t.Errorf("hash-only snapshot vs same body deviated: %v", obs)
	}
}

func TestSimHash_Properties(t *testing.T) {
	if got := SimHash(nil); got != 0 {
		t.Errorf("SimHash(nil) = %d, want 0", got)
	}
	if got := SimHash([]byte(" ,.;<>")); got != 0 {
		t.Errorf("SimHash(punctuation only) = %d, want 0", got)
	}
	if SimHash([]byte("Hello World")) != SimHash([]byte("hello   world")) {
		t.Error("SimHash should ignore case and separators")
	}
	if SimHash([]byte(`{"error":"not found"}`)) == SimHash([]byte(`{"id":1,"name":"x"}`)) {
		t.Error("distinct JSON bodies hashed equal")
	}
}

// softServer answers 200 with the same catch-all page for every path except
// /admin, which is a real, different page.
func softServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin" {
			_, _ = fmt.Fprint(w, realPage)
			return
		}
		_, _ = fmt.Fprintf(w, softNotFoundPage, html.EscapeString(r.URL.Path))
	}))
}

func getBuilder(url string) probe.RequestBuilder {
	return func(ctx context.Context) (*http.Request, error) {
		return probe.NewRequest(ctx, http.MethodGet, url, nil)
	}
}

func TestCaptureSnapshot(t *testing.T) {
	srv := softServer()
	defer srv.Close()

	s, err := CaptureSnapshot(context.Background(), probe.New(), getBuilder(srv.URL+"/random-9f3a"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.StatusCode != http.StatusOK || len(s.Body) == 0 || s.BodyHash != SimHash(s.Body) || s.BodyHash == 0 {
		t.Errorf("snapshot = status %d, %d body bytes, hash %d", s.StatusCode, len(s.Body), s.BodyHash)
	}
}

func TestCaptureSnapshot_BuildError(t *testing.T) {
	boom := fmt.Errorf("boom")
	_, err := CaptureSnapshot(context.Background(), probe.New(), func(context.Context) (*http.Request, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
}

func TestProbeAndCompareBaseline_Soft404(t *testing.T) {
	srv := softServer()
	defer srv.Close()
	p := probe.New()

	// Baseline: a path that certainly does not exist. The server still says 200.
	base, err := CaptureSnapshot(context.Background(), p, getBuilder(srv.URL+"/does-not-exist-7c1e"))
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	store := NewStaticResultStore(ResultData(testCheckIDProbe, base))
	similar := CompareSimilarity(0.8)

	// Another missing path: same catch-all page, no deviation under similarity.
	_, vulnerable, err := ProbeAndCompareBaseline(context.Background(), p, getBuilder(srv.URL+"/backup.zip"), store, testCheckIDProbe, similar)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vulnerable {
		t.Error("soft-404 reported as deviation under CompareSimilarity")
	}

	// A genuinely different page deviates.
	_, vulnerable, err = ProbeAndCompareBaseline(context.Background(), p, getBuilder(srv.URL+"/admin"), store, testCheckIDProbe, similar)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !vulnerable {
		t.Error("real page not reported as deviation under CompareSimilarity")
	}

	// Status-only default is unchanged: both are 200, so neither deviates.
	_, vulnerable, err = ProbeAndCompareBaseline(context.Background(), p, getBuilder(srv.URL+"/admin"), store, testCheckIDProbe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vulnerable {
		t.Error("default comparator should be status-only")
	}
}

func TestProbeAndCompareBaseline_RealVsMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.env" {
			_, _ = fmt.Fprint(w, "DB_PASSWORD=hunter2\nAPI_KEY=abc123\n")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	p := probe.New()

	base, err := CaptureSnapshot(context.Background(), p, getBuilder(srv.URL+"/nope-31ab"))
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	store := NewStaticResultStore(ResultData(testCheckIDProbe, base))

	for name, cmp := range map[string]BaselineComparator{
		"status":     CompareStatusCode,
		"length":     CompareStatusAndLength(0.1),
		"similarity": CompareSimilarity(0.9),
	} {
		_, vulnerable, err := ProbeAndCompareBaseline(context.Background(), p, getBuilder(srv.URL+"/.env"), store, testCheckIDProbe, cmp)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !vulnerable {
			t.Errorf("%s: existing file not reported as deviation", name)
		}
	}
}

func TestProbeAndCompareBaseline_ComparatorsCompose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, realPage)
	}))
	defer srv.Close()

	store := NewStaticResultStore(ResultData(testCheckIDProbe, snap(200, fmt.Sprintf(softNotFoundPage, "/x"))))
	never := func(Snapshot, Snapshot) []Observation { return nil }
	_, vulnerable, err := ProbeAndCompareBaseline(context.Background(), probe.New(), getBuilder(srv.URL), store, testCheckIDProbe, never, CompareSimilarity(0.9))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !vulnerable {
		t.Error("any comparator flagging should mark the response as deviating")
	}
}
