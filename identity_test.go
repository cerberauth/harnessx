package harnessx

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	alice = "alice"
	bob   = "bob"
	admin = "admin"

	roleKey       = "role"
	idCaptureEach = "capture-each"
)

var testIdentities = []Identity{
	{Name: alice, Metadata: map[string]string{roleKey: "user"}, Data: "token-a"},
	{Name: bob, Metadata: map[string]string{roleKey: "user"}, Data: "token-b"},
	{Name: admin, Metadata: map[string]string{roleKey: admin}, Data: "token-admin"},
}

func discoverCheck(ids ...string) Check {
	return Check{ID: idDiscover, Run: func(context.Context, Target, ResultStore) (Result, error) {
		res := make([]Resource, 0, len(ids))
		for _, id := range ids {
			res = append(res, Resource{ID: id})
		}
		return Result{Resources: res}, nil
	}}
}

func identityResult(t *testing.T, summary ScanSummary, id CheckID, resourceID string) Result {
	t.Helper()
	for _, r := range summary.Results {
		if r.CheckID == id && r.ResourceID == resourceID {
			return r
		}
	}
	t.Fatalf("no result for %s/%s", id, resourceID)
	return Result{}
}

func TestIdentities_NoneConfiguredKeepsBehaviour(t *testing.T) {
	calls := 0
	e := New(WithChecks(Check{ID: "plain", Run: func(ctx context.Context, _ Target, _ ResultStore) (Result, error) {
		calls++
		if _, ok := IdentityFrom(ctx); ok {
			t.Error("unexpected identity in context")
		}
		return Result{}, nil
	}}))
	summary, err := e.Run(context.Background(), Target{Identities: testIdentities})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || summary.Executed != 1 || len(summary.Results[0].Attempts) != 0 || summary.Results[0].Identities != nil {
		t.Errorf("calls = %d, summary = %+v", calls, summary)
	}
}

func TestIdentities_SkipWhenInsufficient(t *testing.T) {
	noop := func(context.Context, Target, ResultStore) (Result, error) { return Result{}, nil }
	e := New(WithChecks(
		Check{ID: "pairs", IdentityMode: IdentityPairs, Run: noop},
		Check{ID: idCaptureEach, IdentityMode: IdentitiesEach, Run: noop},
		Check{ID: "three", RequiresIdentities: 3, Run: noop},
	))

	summary, err := e.Run(context.Background(), Target{Identities: testIdentities[:1]})
	if err != nil {
		t.Fatal(err)
	}
	want := map[CheckID]string{
		"pairs": "requires 2 identities, 1 configured",
		"three": "requires 3 identities, 1 configured",
	}
	for _, r := range summary.Results {
		if r.SkipReason != want[r.CheckID] {
			t.Errorf("%s: skip reason = %q, want %q", r.CheckID, r.SkipReason, want[r.CheckID])
		}
	}

	summary, err = e.Run(context.Background(), Target{})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Skipped != 3 {
		t.Errorf("Skipped = %d, want 3", summary.Skipped)
	}
}

func TestIdentities_PairFanOutOrdering(t *testing.T) {
	var running, peak int32
	e := New(WithChecks(
		discoverCheck("r1", "r2"),
		Check{
			ID:           "bola",
			Scope:        ScopePerResource,
			DependsOn:    []CheckID{idDiscover},
			IdentityMode: IdentityPairs,
			Concurrency:  2,
			RunResource: func(ctx context.Context, _ Target, res Resource, _ ResultStore) (Result, error) {
				n := atomic.AddInt32(&running, 1)
				defer atomic.AddInt32(&running, -1)
				for {
					p := atomic.LoadInt32(&peak)
					if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				pair, ok := IdentityPairFrom(ctx)
				if !ok {
					return Result{}, errors.New("no pair")
				}
				obs := []Observation{{Title: pair.First.Data.(string) + ">" + pair.Second.Data.(string)}}
				return Result{Observations: obs, Data: res.ID + ":" + pair.First.Name + ">" + pair.Second.Name}, nil
			},
		},
	))
	summary, err := e.Run(context.Background(), Target{Identities: testIdentities})
	if err != nil {
		t.Fatal(err)
	}
	if peak > 2 {
		t.Errorf("peak concurrency = %d, want at most 2 across all pairs and resources", peak)
	}

	wantPairs := [][]string{
		{alice, bob}, {alice, admin},
		{bob, alice}, {bob, admin},
		{admin, alice}, {admin, bob},
	}
	for _, resID := range []string{"r1", "r2"} {
		r := identityResult(t, summary, "bola", resID)
		if len(r.Attempts) != len(wantPairs) {
			t.Fatalf("%s: %d attempts, want %d", resID, len(r.Attempts), len(wantPairs))
		}
		for i, a := range r.Attempts {
			if !reflect.DeepEqual(a.Identities, wantPairs[i]) {
				t.Errorf("%s attempt %d identities = %v, want %v", resID, i, a.Identities, wantPairs[i])
			}
			if want := resID + ":" + wantPairs[i][0] + ">" + wantPairs[i][1]; a.Data != want {
				t.Errorf("%s attempt %d data = %v, want %s", resID, i, a.Data, want)
			}
		}
		for i, o := range r.Observations {
			if !reflect.DeepEqual(o.Identities, wantPairs[i]) || o.CheckID != "bola" || o.ResourceID != resID {
				t.Errorf("%s observation %d = %+v", resID, i, o)
			}
		}
	}
	if summary.Executed != 3 {
		t.Errorf("Executed = %d, want 3 (discover + 2 resources)", summary.Executed)
	}
}

func TestIdentities_StoreLookups(t *testing.T) {
	var got sync.Map
	e := New(WithChecks(
		discoverCheck("r1"),
		Check{
			ID:           "snapshot-resp",
			Scope:        ScopePerResource,
			DependsOn:    []CheckID{idDiscover},
			IdentityMode: IdentitiesEach,
			RunResource: func(ctx context.Context, _ Target, res Resource, _ ResultStore) (Result, error) {
				ident, _ := IdentityFrom(ctx)
				return Result{Data: res.ID + " as " + ident.Name}, nil
			},
		},
		Check{
			ID:           "global-role",
			IdentityMode: IdentitiesEach,
			Run: func(ctx context.Context, _ Target, _ ResultStore) (Result, error) {
				ident, _ := IdentityFrom(ctx)
				return Result{Data: ident.Metadata[roleKey]}, nil
			},
		},
		Check{
			ID:           "cross-compare",
			Scope:        ScopePerResource,
			DependsOn:    []CheckID{"snapshot-resp", "global-role"},
			IdentityMode: IdentityPairs,
			RunResource: func(ctx context.Context, _ Target, res Resource, store ResultStore) (Result, error) {
				pair, _ := IdentityPairFrom(ctx)
				first, ok := GetForIdentity(store, "snapshot-resp", res.ID, pair.First.Name)
				if !ok {
					return Result{}, errors.New("missing first")
				}
				role, ok := GetForIdentity(store, "global-role", "", pair.Second.Name)
				if !ok {
					return Result{}, errors.New("missing global")
				}
				got.Store(pair.First.Name+">"+pair.Second.Name, fmt.Sprint(first.Data, "|", role.Data))
				return Result{}, nil
			},
		},
	))
	summary, err := e.Run(context.Background(), Target{Identities: testIdentities[1:]})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range summary.Results {
		if r.Err != nil {
			t.Fatalf("%s: %v", r.CheckID, r.Err)
		}
	}
	for key, want := range map[string]string{"bob>admin": "r1 as bob|admin", "admin>bob": "r1 as admin|user"} {
		if v, _ := got.Load(key); v != want {
			t.Errorf("%s = %v, want %s", key, v, want)
		}
	}

	if r := identityResult(t, summary, "snapshot-resp", "r1"); len(r.Attempts) != 2 || r.Data != nil {
		t.Errorf("fetch aggregate = %+v", r)
	}
}

func TestIdentities_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	e := New(WithChecks(
		discoverCheck("r1"),
		Check{
			ID:           "slow",
			Scope:        ScopePerResource,
			DependsOn:    []CheckID{idDiscover},
			IdentityMode: IdentityPairs,
			Concurrency:  1,
			RunResource: func(ctx context.Context, _ Target, _ Resource, _ ResultStore) (Result, error) {
				calls.Add(1)
				cancel()
				<-ctx.Done()
				return Result{}, ctx.Err()
			},
		},
	))
	summary, err := e.Run(ctx, Target{Identities: testIdentities})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("calls = %d, want 1", n)
	}
	r := identityResult(t, summary, "slow", "r1")
	if len(r.Attempts) != 6 || !errors.Is(r.Err, context.Canceled) {
		t.Fatalf("result = %+v", r)
	}
	for i, a := range r.Attempts {
		if !errors.Is(a.Err, context.Canceled) {
			t.Errorf("attempt %d err = %v", i, a.Err)
		}
	}
}

func TestIdentities_InvalidNames(t *testing.T) {
	e := New(WithChecks(Check{ID: "x", Run: func(context.Context, Target, ResultStore) (Result, error) { return Result{}, nil }}))
	for _, ids := range [][]Identity{{{Name: ""}}, {{Name: "a"}, {Name: "a"}}} {
		if _, err := e.Run(context.Background(), Target{Identities: ids}); !errors.Is(err, ErrInvalidIdentity) {
			t.Errorf("identities %v: err = %v, want ErrInvalidIdentity", ids, err)
		}
	}
}

func TestIdentities_ValidateRejectsVariants(t *testing.T) {
	err := New().Register(Check{ID: "v", Variants: []string{"a"}, IdentityMode: IdentitiesEach,
		RunVariant: func(context.Context, Target, string, ResultStore) (Result, error) { return Result{}, nil }})
	if err == nil {
		t.Error("expected an error for Variants with an IdentityMode")
	}
}

func TestIdentities_BaselineHelpers(t *testing.T) {
	status := map[string]int{alice: 200, bob: 403}
	capture := func(ctx context.Context, _ Target, _ Resource, _ ResultStore) (Snapshot, error) {
		ident, ok := IdentityFrom(ctx)
		if !ok {
			pair, _ := IdentityPairFrom(ctx)
			ident = pair.Second
		}
		return Snapshot{StatusCode: status[ident.Name]}, nil
	}
	e := New(WithChecks(
		discoverCheck("r1"),
		NewCaptureBaselineCheck(CaptureBaselineCheckConfig{ID: "as-alice", DependsOn: []CheckID{idDiscover}, Identity: alice, Capture: capture}),
		NewCaptureBaselineCheck(CaptureBaselineCheckConfig{ID: "as-carol", DependsOn: []CheckID{idDiscover}, Identity: "carol", Capture: capture}),
		NewCaptureBaselineCheck(CaptureBaselineCheckConfig{ID: idCaptureEach, DependsOn: []CheckID{idDiscover}, IdentityMode: IdentitiesEach, Capture: capture}),
		NewBaselineCheck(BaselineCheckConfig{
			ID:           "cross",
			DependsOn:    []CheckID{idCaptureEach},
			IdentityMode: IdentityPairs,
			Baseline:     BaselineFromIdentityCheck(idCaptureEach),
			Capture:      capture,
		}),
		NewBaselineCheck(BaselineCheckConfig{
			ID:        "bob-vs-alice",
			DependsOn: []CheckID{"as-alice"},
			Identity:  bob,
			Baseline:  BaselineFromIdentityCheck("as-alice", alice),
			Capture:   capture,
		}),
	))
	summary, err := e.Run(context.Background(), Target{Identities: testIdentities[:2]})
	if err != nil {
		t.Fatal(err)
	}

	if r := identityResult(t, summary, "as-carol", "r1"); r.SkipReason != `identity "carol" not configured` {
		t.Errorf("as-carol = %+v", r)
	}
	if r := identityResult(t, summary, "as-alice", "r1"); !reflect.DeepEqual(r.Identities, []string{alice}) {
		t.Errorf("as-alice identities = %v", r.Identities)
	}
	cross := identityResult(t, summary, "cross", "r1")
	if len(cross.Attempts) != 2 || len(cross.Attempts[0].Observations) != 1 || len(cross.Attempts[1].Observations) != 1 {
		t.Errorf("cross = %+v", cross)
	}
	if r := identityResult(t, summary, "bob-vs-alice", "r1"); len(r.Observations) != 1 || !reflect.DeepEqual(r.Identities, []string{bob}) {
		t.Errorf("bob-vs-alice = %+v", r)
	}
}

func TestStaticResultStore_GetForIdentity(t *testing.T) {
	store := NewStaticResultStore(Result{CheckID: "c", ResourceID: "r", Identities: []string{"a", "b"}, Data: 1})
	if r, ok := GetForIdentity(store, "c", "r", "a", "b"); !ok || r.Data != 1 {
		t.Errorf("GetForIdentity = %+v, %v", r, ok)
	}
	if _, ok := GetForIdentity(store, "c", "r", "b", "a"); ok {
		t.Error("pair order ignored")
	}
	if _, ok := GetForIdentity(plainStore{store}, "c", "r", "a", "b"); ok {
		t.Error("store without identity support should report false")
	}
}

type plainStore struct{ ResultStore }
