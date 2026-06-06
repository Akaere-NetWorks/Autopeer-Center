// Package apiversion implements Stripe-style, header-negotiated API versioning.
//
// Handlers always build the LATEST canonical response shape. Clients pin to an
// older dated version via the Autopeer-Version request header; a chain of
// backward "version change" transformers (see changes.go) then downgrades the
// latest response to the shape it had at the requested version.
//
// This is orthogonal to the URL path versioning under /api/v1: it layers on
// top of the /api/v1 routes.
//
// Adding the next version change (the extension recipe):
//  1. Append the new dated string to `versions` (it becomes the new Latest()).
//  2. Update that resource's handler *Resp structs so the LATEST shape is
//     uniform across all of its endpoints.
//  3. Register a VersionChange in changes.go whose Version is the new date and
//     whose Apply reshapes a resource object BACK to the prior shape
//     (delete/rename/convert fields).
//  4. Add a Resource constant if it's a new resource, and call
//     handler.JSONVersioned(..., apiversion.ResourceX, listKey, data) at its
//     write sites.
//  5. Add Downgrade tests: strips for older, identity for latest, chain order.
package apiversion

import (
	"sort"
	"strings"
)

// versions lists every known API version in chronological order, oldest first.
// The format is YYYY-MM-DD so lexicographic comparison equals chronological
// comparison. The last element is the latest (current) version. This slice is
// the single source of truth — Latest(), IsKnown, ordering all derive from it.
var versions = []string{
	"2025-01-01", // baseline: before peer endpoint_mismatch_since / bgp_suspended_by_endpoint existed
	"2026-06-06", // peer objects include the two endpoint/BGP fields
	"2026-06-07", // latest: peer objects include wg_preshared_key
}

// Latest returns the newest known API version.
func Latest() string { return versions[len(versions)-1] }

// Versions returns a copy of the known versions, oldest first.
func Versions() []string {
	out := make([]string, len(versions))
	copy(out, versions)
	return out
}

// IsKnown reports whether v is a recognized API version.
func IsKnown(v string) bool {
	for _, x := range versions {
		if x == v {
			return true
		}
	}
	return false
}

// Resolve maps a raw Autopeer-Version header value to the version to use.
// An empty/whitespace header resolves to Latest(). A known version resolves to
// itself. An unknown version returns ("", false) so the caller can reject it.
func Resolve(header string) (string, bool) {
	h := strings.TrimSpace(header)
	if h == "" {
		return Latest(), true
	}
	if IsKnown(h) {
		return h, true
	}
	return "", false
}

// Resource identifies the kind of object a version change transforms.
type Resource string

const (
	// ResourcePeer is a peer object as returned by the peer endpoints.
	ResourcePeer Resource = "peer"
)

// Transform downgrades a single resource object (decoded as map[string]any) in
// place, from its version to the immediately prior shape.
type Transform func(obj map[string]any)

// VersionChange describes one backward transformation: the version at which a
// shape change was introduced, the resource it applies to, and how to undo it.
type VersionChange struct {
	Version     string
	Resource    Resource
	Description string
	Apply       Transform
}

// changes holds every registered version change, in registration order.
var changes []VersionChange

// register adds a version change to the registry. Called from changes.go init().
func register(c VersionChange) { changes = append(changes, c) }

// changesForResourceNewestFirst returns the registered changes for a resource,
// sorted by Version descending (newest first) so they can be applied in
// downgrade order.
func changesForResourceNewestFirst(res Resource) []VersionChange {
	var out []VersionChange
	for _, c := range changes {
		if c.Resource == res {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out
}

// Downgrade transforms a generic JSON value (a single object, a list, or a
// wrapper map) for resource res from the latest shape down to target.
//
// listKey names the field holding resource objects when v is a wrapper map
// (e.g. "peers" for {"peers":[...], "total":N}); pass "" when v is a bare
// object or a bare list. Every change whose Version is newer than target is
// applied, newest-first. When target is the latest version this is a no-op fast
// path. v is mutated in place and also returned for convenience.
func Downgrade(res Resource, target string, v any, listKey string) any {
	if target == Latest() {
		return v
	}
	for _, c := range changesForResourceNewestFirst(res) {
		if c.Version > target {
			applyToValue(v, listKey, c.Apply)
		}
	}
	return v
}

// applyToValue applies fn to every resource object inside v. It handles a bare
// object, a bare list, and a wrapper map (descending into listKey). Non-object
// list elements are skipped so a malformed payload cannot panic.
func applyToValue(v any, listKey string, fn Transform) {
	switch t := v.(type) {
	case map[string]any:
		if listKey != "" {
			if inner, ok := t[listKey]; ok {
				applyToValue(inner, "", fn)
			}
			return
		}
		fn(t)
	case []any:
		for _, el := range t {
			if obj, ok := el.(map[string]any); ok {
				fn(obj)
			}
		}
	}
}
