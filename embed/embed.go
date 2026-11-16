// Package embed registers the checks of a check pack (the checks another
// harnessx-based tool ships) in a host engine, converting scope, scoping the
// result store to the current resource and namespacing check IDs, so the
// host only has to build the pack's context.
package embed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/cerberauth/harnessx"
)

// ContextFunc builds the pack context, the value pack checks read from
// Target.Data. resource is nil when the host scope is ScopeGlobal.
type ContextFunc func(ctx context.Context, target harnessx.Target, resource *harnessx.Resource) (any, error)

// Options configures how pack checks are wrapped for the host engine.
type Options struct {
	// Scope is the host scope every wrapped check runs in.
	Scope harnessx.CheckScope

	// Prefix is prepended to every pack check ID and DependsOn entry, then
	// Rename, when set, maps the result to the final host ID.
	Prefix string
	Rename func(harnessx.CheckID) harnessx.CheckID

	// Context builds the pack context once per host resource, or once for a
	// ScopeGlobal host, in an internal check every wrapped check depends on.
	// When nil, pack checks receive the host Target.Data unchanged.
	Context ContextFunc
	// ContextID is the ID of that internal check. It defaults to
	// "<namespace>.context", namespace being the part of the first wrapped ID
	// before its first dot.
	ContextID harnessx.CheckID

	// Resource is the resource a ScopePerResource pack check runs against in
	// a ScopeGlobal host. It defaults to one GET resource with ID "target" and
	// the target URL.
	Resource func(target harnessx.Target) harnessx.Resource

	// DependsOn is added to every wrapped check, and Skip is evaluated before
	// the pack check's own skip decision and conditions.
	DependsOn []harnessx.CheckID
	Skip      harnessx.SkipDecision
}

// TargetResourceID is the ID of the default resource used to run a
// ScopePerResource pack check in a ScopeGlobal host.
const TargetResourceID = "target"

func defaultResource(target harnessx.Target) harnessx.Resource {
	return harnessx.Resource{ID: TargetResourceID, URL: target.URL, Method: http.MethodGet}
}

// Checks wraps pack for the host engine described by opts. The pack's
// DependsOn graph, skip decisions, conditions, variants and Internal markers
// are kept; only IDs, scope and what each check sees change.
func Checks(pack []harnessx.Check, opts Options) ([]harnessx.Check, error) {
	if opts.Scope != harnessx.ScopeGlobal && opts.Scope != harnessx.ScopePerResource {
		return nil, fmt.Errorf("embed: invalid host Scope %d", opts.Scope)
	}
	if len(pack) == 0 {
		return nil, errors.New("embed: no pack checks")
	}
	w := &wrapper{opts: opts}
	if w.opts.Resource == nil {
		w.opts.Resource = defaultResource
	}

	if opts.Context != nil {
		w.contextID = opts.ContextID
		if w.contextID == "" {
			first := string(w.id(pack[0].ID))
			ns, _, ok := strings.Cut(first, ".")
			if !ok || ns == "" {
				return nil, fmt.Errorf("embed: cannot derive ContextID from %q, set Options.ContextID", first)
			}
			w.contextID = harnessx.CheckID(ns + ".context")
		}
	}

	out := make([]harnessx.Check, 0, len(pack)+1)
	if w.contextID != "" {
		out = append(out, w.contextCheck())
	}
	for _, pc := range pack {
		out = append(out, w.wrap(pc))
	}
	return out, nil
}

// MustChecks is like Checks but panics on error.
func MustChecks(pack []harnessx.Check, opts Options) []harnessx.Check {
	checks, err := Checks(pack, opts)
	if err != nil {
		panic(err)
	}
	return checks
}

// Select returns the checks of pack named by ids plus every check they
// transitively depend on, in pack order.
func Select(pack []harnessx.Check, ids ...harnessx.CheckID) ([]harnessx.Check, error) {
	byID := make(map[harnessx.CheckID]harnessx.Check, len(pack))
	for _, c := range pack {
		byID[c.ID] = c
	}
	keep := make(map[harnessx.CheckID]bool, len(ids))
	var visit func(id harnessx.CheckID) error
	visit = func(id harnessx.CheckID) error {
		if keep[id] {
			return nil
		}
		c, ok := byID[id]
		if !ok {
			return fmt.Errorf("%w: %q", harnessx.ErrUnknownCheckID, id)
		}
		keep[id] = true
		for _, dep := range c.DependsOn {
			if err := visit(dep); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return slices.DeleteFunc(slices.Clone(pack), func(c harnessx.Check) bool { return !keep[c.ID] }), nil
}

type wrapper struct {
	opts      Options
	contextID harnessx.CheckID
}

func (w *wrapper) id(id harnessx.CheckID) harnessx.CheckID {
	id = harnessx.CheckID(w.opts.Prefix) + id
	if w.opts.Rename != nil {
		id = w.opts.Rename(id)
	}
	return id
}

func (w *wrapper) contextCheck() harnessx.Check {
	c := harnessx.Check{
		ID:        w.contextID,
		Name:      "Pack Context",
		Internal:  true,
		Scope:     w.opts.Scope,
		DependsOn: slices.Clone(w.opts.DependsOn),
		Skip:      w.opts.Skip,
	}
	build := w.opts.Context
	if w.opts.Scope == harnessx.ScopePerResource {
		c.RunResource = func(ctx context.Context, target harnessx.Target, resource harnessx.Resource, _ harnessx.ResultStore) (harnessx.Result, error) {
			data, err := build(ctx, target, &resource)
			return harnessx.Result{Data: data}, err
		}
		return c
	}
	c.Run = func(ctx context.Context, target harnessx.Target, _ harnessx.ResultStore) (harnessx.Result, error) {
		data, err := build(ctx, target, nil)
		return harnessx.Result{Data: data}, err
	}
	return c
}

// call is everything one invocation of a wrapped check hands to the pack
// check: its target, its resource (nil for a ScopeGlobal pack check) and its
// store view.
type call struct {
	target   harnessx.Target
	resource *harnessx.Resource
	store    harnessx.ResultStore
}

// prepare builds the call for one host invocation; hostRes is nil in a
// ScopeGlobal host.
func (w *wrapper) prepare(pc harnessx.Check, target harnessx.Target, hostRes *harnessx.Resource, store harnessx.ResultStore) (call, error) {
	inner := target
	if hostRes != nil && hostRes.URL != "" {
		inner.URL = hostRes.URL
	}
	if w.contextID != "" {
		var (
			r  harnessx.Result
			ok bool
		)
		if hostRes != nil {
			r, ok = store.GetForResource(w.contextID, hostRes.ID)
		} else {
			r, ok = store.Get(w.contextID)
		}
		switch {
		case !ok:
			return call{}, errors.New("pack context unavailable")
		case r.Err != nil:
			return call{}, fmt.Errorf("pack context unavailable: %w", r.Err)
		case r.Skipped:
			return call{}, fmt.Errorf("pack context unavailable: %s", r.SkipReason)
		}
		inner.Data = r.Data
	}

	v := &view{host: store, id: w.id}
	c := call{target: inner, store: v}
	switch {
	case pc.Scope == harnessx.ScopeGlobal && hostRes != nil:
		v.resourceID = hostRes.ID
	case pc.Scope == harnessx.ScopePerResource && hostRes == nil:
		res := w.opts.Resource(target)
		v.single = &res
		c.resource = &res
	case pc.Scope == harnessx.ScopePerResource:
		c.resource = hostRes
	}
	return c, nil
}

func (w *wrapper) skip(ctx context.Context, pc harnessx.Check, c call) string {
	var reason string
	if c.resource != nil {
		reason = pc.Skip.EvalResource(ctx, c.target, *c.resource, c.store)
	} else {
		reason = pc.Skip.Eval(ctx, c.target, c.store)
	}
	if reason != "" {
		return reason
	}
	for _, cond := range pc.Conditions {
		if !cond(c.store) {
			return "condition not met"
		}
	}
	return ""
}

func (w *wrapper) wrap(pc harnessx.Check) harnessx.Check {
	hc := pc
	hc.ID = w.id(pc.ID)
	hc.Scope = w.opts.Scope
	hc.Conditions = nil
	hc.Run, hc.RunResource, hc.RunVariant, hc.RunResourceVariant = nil, nil, nil, nil

	hc.DependsOn = make([]harnessx.CheckID, 0, len(pc.DependsOn)+len(w.opts.DependsOn)+1)
	if w.contextID != "" {
		hc.DependsOn = append(hc.DependsOn, w.contextID)
	}
	hc.DependsOn = append(hc.DependsOn, w.opts.DependsOn...)
	for _, dep := range pc.DependsOn {
		hc.DependsOn = append(hc.DependsOn, w.id(dep))
	}
	hc.DependsOn = slices.Compact(hc.DependsOn)

	run := func(ctx context.Context, c call, variant string) (harnessx.Result, error) {
		var (
			r   harnessx.Result
			err error
		)
		switch {
		case len(pc.Variants) > 0 && c.resource != nil:
			r, err = pc.RunResourceVariant(ctx, c.target, *c.resource, variant, c.store)
		case len(pc.Variants) > 0:
			r, err = pc.RunVariant(ctx, c.target, variant, c.store)
		case c.resource != nil:
			r, err = pc.RunResource(ctx, c.target, *c.resource, c.store)
		default:
			r, err = pc.Run(ctx, c.target, c.store)
		}
		w.renameObservations(&r)
		return r, err
	}

	if w.opts.Scope == harnessx.ScopePerResource {
		hc.Skip = harnessx.SkipResourceWhen(func(ctx context.Context, target harnessx.Target, res harnessx.Resource, store harnessx.ResultStore) string {
			if reason := w.opts.Skip.EvalResource(ctx, target, res, store); reason != "" {
				return reason
			}
			c, err := w.prepare(pc, target, &res, store)
			if err != nil {
				return err.Error()
			}
			return w.skip(ctx, pc, c)
		})
		if len(pc.Variants) > 0 {
			hc.RunResourceVariant = func(ctx context.Context, target harnessx.Target, res harnessx.Resource, variant string, store harnessx.ResultStore) (harnessx.Result, error) {
				c, err := w.prepare(pc, target, &res, store)
				if err != nil {
					return harnessx.Result{}, err
				}
				return run(ctx, c, variant)
			}
		} else {
			hc.RunResource = func(ctx context.Context, target harnessx.Target, res harnessx.Resource, store harnessx.ResultStore) (harnessx.Result, error) {
				c, err := w.prepare(pc, target, &res, store)
				if err != nil {
					return harnessx.Result{}, err
				}
				return run(ctx, c, "")
			}
		}
		return hc
	}

	hc.Skip = harnessx.SkipWhen(func(ctx context.Context, target harnessx.Target, store harnessx.ResultStore) string {
		if reason := w.opts.Skip.Eval(ctx, target, store); reason != "" {
			return reason
		}
		c, err := w.prepare(pc, target, nil, store)
		if err != nil {
			return err.Error()
		}
		return w.skip(ctx, pc, c)
	})
	if len(pc.Variants) > 0 {
		hc.RunVariant = func(ctx context.Context, target harnessx.Target, variant string, store harnessx.ResultStore) (harnessx.Result, error) {
			c, err := w.prepare(pc, target, nil, store)
			if err != nil {
				return harnessx.Result{}, err
			}
			return run(ctx, c, variant)
		}
	} else {
		hc.Run = func(ctx context.Context, target harnessx.Target, store harnessx.ResultStore) (harnessx.Result, error) {
			c, err := w.prepare(pc, target, nil, store)
			if err != nil {
				return harnessx.Result{}, err
			}
			return run(ctx, c, "")
		}
	}
	return hc
}

func (w *wrapper) renameObservations(r *harnessx.Result) {
	if len(r.Observations) == 0 {
		return
	}
	obs := slices.Clone(r.Observations)
	for i := range obs {
		if obs[i].CheckID != "" {
			obs[i].CheckID = w.id(obs[i].CheckID)
		}
	}
	r.Observations = obs
}

// view is the result store a wrapped pack check sees: pack IDs are mapped to
// host IDs, a global pack check in a per-resource host reads the results of
// the current resource, and a per-resource pack check in a global host sees
// its single resource.
type view struct {
	host       harnessx.ResultStore
	id         func(harnessx.CheckID) harnessx.CheckID
	resourceID string
	single     *harnessx.Resource
}

func (v *view) Get(id harnessx.CheckID) (harnessx.Result, bool) {
	if v.resourceID != "" {
		return v.host.GetForResource(v.id(id), v.resourceID)
	}
	return v.host.Get(v.id(id))
}

func (v *view) GetForResource(id harnessx.CheckID, resourceID string) (harnessx.Result, bool) {
	if v.single != nil {
		if resourceID != v.single.ID {
			return harnessx.Result{}, false
		}
		return v.host.Get(v.id(id))
	}
	return v.host.GetForResource(v.id(id), resourceID)
}

func (v *view) Observations() []harnessx.Observation { return v.host.Observations() }

func (v *view) Resources() []harnessx.Resource {
	if v.single != nil {
		return []harnessx.Resource{*v.single}
	}
	return v.host.Resources()
}
