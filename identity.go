package harnessx

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Identity is one named credential set a scan can act as, such as a user of a
// given role or tenant. Data is opaque to the engine; the consumer interprets
// it (a token, a cookie jar, a probe mutator, ...).
type Identity struct {
	Name     string
	Metadata map[string]string
	Data     any
}

// IdentityPair is an ordered pair of distinct identities, as handed to a check
// with IdentityPairs: typically First owns or is allowed something and Second
// tries to reach it.
type IdentityPair struct {
	First  Identity
	Second Identity
}

// IdentityMode selects whether, and how, the engine fans a check out over the
// identities of the target.
type IdentityMode int

const (
	// IdentitiesNone runs the check once, as today (the default).
	IdentitiesNone IdentityMode = iota
	// IdentitiesEach runs the check once per identity (per resource, for a
	// ScopePerResource check). The identity is read with IdentityFrom.
	IdentitiesEach
	// IdentityPairs runs the check once per ordered pair of distinct
	// identities (per resource, for a ScopePerResource check). The pair is
	// read with IdentityPairFrom.
	IdentityPairs
)

// ErrInvalidIdentity is returned by Run and RunScenario when a target identity
// has an empty or duplicate name.
var ErrInvalidIdentity = errors.New("harnessx: invalid identity")

type identityKey struct{}

type identityPairKey struct{}

// ContextWithIdentity returns ctx carrying identity, as the engine passes it to
// a check with IdentitiesEach.
func ContextWithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identity)
}

// ContextWithIdentityPair returns ctx carrying pair, as the engine passes it to
// a check with IdentityPairs.
func ContextWithIdentityPair(ctx context.Context, pair IdentityPair) context.Context {
	return context.WithValue(ctx, identityPairKey{}, pair)
}

// IdentityFrom returns the identity a check runs as, and whether there is one.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// IdentityPairFrom returns the identity pair a check runs for, and whether
// there is one.
func IdentityPairFrom(ctx context.Context) (IdentityPair, bool) {
	pair, ok := ctx.Value(identityPairKey{}).(IdentityPair)
	return pair, ok
}

// IdentityByName returns the identity of target named name.
func (t Target) IdentityByName(name string) (Identity, bool) {
	for _, id := range t.Identities {
		if id.Name == name {
			return id, true
		}
	}
	return Identity{}, false
}

func validateIdentities(identities []Identity) error {
	seen := make(map[string]bool, len(identities))
	for i, id := range identities {
		if id.Name == "" {
			return fmt.Errorf("%w: identity #%d has no name", ErrInvalidIdentity, i+1)
		}
		if seen[id.Name] {
			return fmt.Errorf("%w: duplicate name %q", ErrInvalidIdentity, id.Name)
		}
		seen[id.Name] = true
	}
	return nil
}

// identityRun is one unit of an identity fan-out: one identity, or one ordered
// pair.
type identityRun struct {
	names []string
	bind  func(context.Context) context.Context
}

func identityRuns(mode IdentityMode, identities []Identity) []identityRun {
	var runs []identityRun
	switch mode {
	case IdentitiesEach:
		for _, id := range identities {
			runs = append(runs, identityRun{
				names: []string{id.Name},
				bind:  func(ctx context.Context) context.Context { return ContextWithIdentity(ctx, id) },
			})
		}
	case IdentityPairs:
		for _, first := range identities {
			for _, second := range identities {
				if first.Name == second.Name {
					continue
				}
				pair := IdentityPair{First: first, Second: second}
				runs = append(runs, identityRun{
					names: []string{first.Name, second.Name},
					bind:  func(ctx context.Context) context.Context { return ContextWithIdentityPair(ctx, pair) },
				})
			}
		}
	}
	return runs
}

// requiredIdentities is how many identities c needs to run.
func (c Check) requiredIdentities() int {
	n := c.RequiresIdentities
	switch {
	case c.IdentityMode == IdentityPairs && n < 2:
		n = 2
	case c.IdentityMode == IdentitiesEach && n < 1:
		n = 1
	}
	return n
}

func identitySkipReason(c Check, target Target) string {
	need := c.requiredIdentities()
	if have := len(target.Identities); have < need {
		return fmt.Sprintf("requires %d identities, %d configured", need, have)
	}
	return ""
}

// IdentityResultStore is implemented by result stores that keep the results of
// a check with an IdentityMode per identity. Use GetForIdentity rather than
// asserting it.
type IdentityResultStore interface {
	// GetForIdentity returns the result a check produced for resourceID ("" for
	// a ScopeGlobal check) as the given identity, or ordered identity pair.
	GetForIdentity(id CheckID, resourceID string, identities ...string) (Result, bool)
}

// GetForIdentity returns the result check id produced for resourceID ("" for a
// ScopeGlobal check) as the given identity, or ordered identity pair, such as
// the response identity "alice" got for a resource. It reports false if store
// does not keep results per identity.
func GetForIdentity(store ResultStore, id CheckID, resourceID string, identities ...string) (Result, bool) {
	s, ok := store.(IdentityResultStore)
	if !ok {
		return Result{}, false
	}
	return s.GetForIdentity(id, resourceID, identities...)
}

type identityResultKey struct {
	checkID    CheckID
	resourceID string
	identities string
}

func newIdentityResultKey(id CheckID, resourceID string, identities []string) identityResultKey {
	return identityResultKey{checkID: id, resourceID: resourceID, identities: strings.Join(identities, "\x00")}
}

func sameIdentities(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
