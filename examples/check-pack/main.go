// Command check-pack embeds a small ScopeGlobal check pack in a host engine
// that runs it once per discovered endpoint.
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/cerberauth/harnessx"
	"github.com/cerberauth/harnessx/embed"
	"github.com/cerberauth/harnessx/probe"
)

// headerpack is a check pack written against a single URL.
type headerpack struct {
	probe *probe.Probe
}

func newHeaderPackContext(p *probe.Probe) *headerpack { return &headerpack{probe: p} }

const (
	idFetch   harnessx.CheckID = "headers.fetch"
	idNoSniff harnessx.CheckID = "headers.content_type_options"
)

func headerPackChecks() []harnessx.Check {
	return []harnessx.Check{
		{
			ID:       idFetch,
			Name:     "Fetch",
			Internal: true,
			Run: func(ctx context.Context, target harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
				pack := target.Data.(*headerpack)
				req, err := probe.NewRequest(ctx, http.MethodGet, target.URL, nil)
				if err != nil {
					return harnessx.Result{}, err
				}
				resp, err := pack.probe.Do(req)
				if err != nil {
					return harnessx.Result{}, err
				}
				defer resp.Body.Close()
				return harnessx.Result{Data: resp.Header}, nil
			},
		},
		{
			ID:        idNoSniff,
			Name:      "X-Content-Type-Options",
			DependsOn: []harnessx.CheckID{idFetch},
			Run: func(_ context.Context, target harnessx.Target, store harnessx.ResultStore) (harnessx.Result, error) {
				headers, _ := harnessx.GetData[http.Header](store, idFetch)
				if headers.Get("X-Content-Type-Options") != "nosniff" {
					return harnessx.Result{Observations: []harnessx.Observation{{Title: "missing nosniff", Evidence: target.URL}}}, nil
				}
				return harnessx.Result{}, nil
			},
		},
	}
}

func run() error {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/safe" {
			w.Header().Set("X-Content-Type-Options", "nosniff")
		}
	}))
	defer srv.Close()

	discover := harnessx.Check{
		ID: "api.discover",
		Run: func(_ context.Context, target harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
			return harnessx.Result{Resources: []harnessx.Resource{
				{ID: "safe", URL: target.URL + "/safe"},
				{ID: "unsafe", URL: target.URL + "/unsafe"},
			}}, nil
		},
	}

	p := probe.New()
	checks, err := embed.Checks(headerPackChecks(), embed.Options{
		Scope:     harnessx.ScopePerResource,
		Prefix:    "proxy.",
		DependsOn: []harnessx.CheckID{discover.ID},
		Context: func(context.Context, harnessx.Target, *harnessx.Resource) (any, error) {
			return newHeaderPackContext(p), nil
		},
	})
	if err != nil {
		return err
	}

	engine := harnessx.New()
	if err := engine.Register(append([]harnessx.Check{discover}, checks...)...); err != nil {
		return err
	}
	summary, err := engine.Run(context.Background(), harnessx.Target{URL: srv.URL})
	if err != nil {
		return err
	}
	for _, r := range summary.Results {
		fmt.Printf("%-40s %-8s %s\n", r.CheckID, r.ResourceID, r.Status())
	}
	for _, o := range summary.Observations {
		fmt.Printf("observation: %s on %s: %s\n", o.CheckID, o.ResourceID, o.Title)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
