package apiversion

// Registered version changes. Each entry's Version is the dated version at which
// the new shape was introduced; its Apply undoes that change, downgrading a
// resource object back to the prior shape. See the extension recipe in the
// package doc (apiversion.go) before adding a new one.
func init() {
	// 2026-06-06: peer objects gained endpoint_mismatch_since and
	// bgp_suspended_by_endpoint. Clients on older versions must not see them.
	register(VersionChange{
		Version:     "2026-06-06",
		Resource:    ResourcePeer,
		Description: "Add endpoint_mismatch_since and bgp_suspended_by_endpoint to peer objects",
		Apply: func(obj map[string]any) {
			delete(obj, "endpoint_mismatch_since")
			delete(obj, "bgp_suspended_by_endpoint")
		},
	})

	// 2026-06-07: peer objects gained wg_preshared_key. Clients on older
	// versions must not see it.
	register(VersionChange{
		Version:     "2026-06-07",
		Resource:    ResourcePeer,
		Description: "Add wg_preshared_key to peer objects",
		Apply: func(obj map[string]any) {
			delete(obj, "wg_preshared_key")
		},
	})
}
