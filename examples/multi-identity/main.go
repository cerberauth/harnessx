// Command multi-identity detects broken object level authorization: every
// user's response for each object is captured, then each ordered pair of users
// is compared, flagging an object one user reads with the other's access.
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/cerberauth/harnessx"
	"github.com/cerberauth/harnessx/probe"
)

const (
	idDiscover harnessx.CheckID = "api.discover"
	idCapture  harnessx.CheckID = "api.capture"
	idBOLA     harnessx.CheckID = "api.bola"
)

const (
	alice = "alice"
	bob   = "bob"
)

var owners = map[string]string{"/invoices/1": alice, "/invoices/2": bob, "/profile/2": bob}

func newAPI() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		owner, ok := owners[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if user != owner && !strings.HasPrefix(r.URL.Path, "/invoices/") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("owned by " + owner))
	}))
}

func requestAs(identity harnessx.Identity, resource harnessx.Resource) probe.RequestBuilder {
	return func(ctx context.Context) (*http.Request, error) {
		return harnessx.NewRequestFromResource(ctx, resource, probe.WithBearerToken(identity.Data.(string)))
	}
}

func checks(p *probe.Probe) []harnessx.Check {
	discover := harnessx.Check{
		ID: idDiscover,
		Run: func(_ context.Context, target harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
			resources := make([]harnessx.Resource, 0, len(owners))
			for path := range owners {
				resources = append(resources, harnessx.Resource{ID: path, URL: target.URL + path, Metadata: map[string]string{"owner": owners[path]}})
			}
			return harnessx.Result{Resources: resources}, nil
		},
	}

	capture := harnessx.NewCaptureBaselineCheck(harnessx.CaptureBaselineCheckConfig{
		ID:           idCapture,
		DependsOn:    []harnessx.CheckID{idDiscover},
		IdentityMode: harnessx.IdentitiesEach,
		Capture: func(ctx context.Context, _ harnessx.Target, resource harnessx.Resource, _ harnessx.ResultStore) (harnessx.Snapshot, error) {
			identity, _ := harnessx.IdentityFrom(ctx)
			return harnessx.CaptureSnapshot(ctx, p, requestAs(identity, resource))
		},
	})

	bola := harnessx.Check{
		ID:           idBOLA,
		Name:         "Broken object level authorization",
		Scope:        harnessx.ScopePerResource,
		DependsOn:    []harnessx.CheckID{idCapture},
		IdentityMode: harnessx.IdentityPairs,
		RunResource: func(ctx context.Context, _ harnessx.Target, resource harnessx.Resource, store harnessx.ResultStore) (harnessx.Result, error) {
			pair, _ := harnessx.IdentityPairFrom(ctx)
			if resource.Metadata["owner"] != pair.First.Name {
				return harnessx.Result{Skipped: true, SkipReason: "first identity does not own the resource"}, nil
			}
			owner, ok := harnessx.GetForIdentity(store, idCapture, resource.ID, pair.First.Name)
			other, ok2 := harnessx.GetForIdentity(store, idCapture, resource.ID, pair.Second.Name)
			if !ok || !ok2 {
				return harnessx.Result{Skipped: true, SkipReason: "no capture"}, nil
			}
			ownerSnap, _ := harnessx.DataAs[harnessx.Snapshot](owner)
			otherSnap, _ := harnessx.DataAs[harnessx.Snapshot](other)
			if otherSnap.StatusCode == http.StatusOK && string(otherSnap.Body) == string(ownerSnap.Body) {
				return harnessx.Result{Observations: []harnessx.Observation{{
					Title:    "Object readable by another user",
					Evidence: fmt.Sprintf("%s read %s", pair.Second.Name, resource.ID),
				}}}, nil
			}
			return harnessx.Result{}, nil
		},
	}
	return []harnessx.Check{discover, capture, bola}
}

func run() error {
	srv := newAPI()
	defer srv.Close()

	engine := harnessx.New(harnessx.WithChecks(checks(probe.New())...))
	summary, err := engine.Run(context.Background(), harnessx.Target{
		URL: srv.URL,
		Identities: []harnessx.Identity{
			{Name: alice, Metadata: map[string]string{"role": "customer"}, Data: alice},
			{Name: bob, Metadata: map[string]string{"role": "customer"}, Data: bob},
		},
	})
	if err != nil {
		return err
	}
	for _, o := range summary.Observations {
		fmt.Printf("%s on %s as %v: %s (%s)\n", o.CheckID, o.ResourceID, o.Identities, o.Title, o.Evidence)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
