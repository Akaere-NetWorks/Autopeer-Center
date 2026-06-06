package handler

import "fmt"

var defaultUserMCPCapabilities = []string{
	"read:nodes",
	"read:peers",
	"read:metrics",
}

var allowedUserMCPCapabilities = map[string]struct{}{
	"read:nodes":                {},
	"read:peers":                {},
	"read:metrics":              {},
	"read:audit":                {},
	"read:preferences":          {},
	"write:peer:create":         {},
	"write:peer:update_pending": {},
	"write:peer:cancel_pending": {},
	"write:operation:create":    {},
	"write:preferences":         {},
}

var defaultAdminMCPCapabilities = []string{
	"admin:read:topology",
	"admin:read:peers",
	"admin:read:metrics",
	"admin:read:audit",
	"admin:read:mcp_keys",
	"admin:read:settings",
}

var allowedAdminMCPCapabilities = map[string]struct{}{
	"admin:read:topology":  {},
	"admin:read:peers":     {},
	"admin:read:metrics":   {},
	"admin:read:audit":     {},
	"admin:read:mcp_keys":  {},
	"admin:read:settings":  {},
	"admin:read:system":    {},
	"admin:read:sensitive": {},
}

func sanitizeCapabilities(requested []string, allowed map[string]struct{}, defaults []string) []string {
	if len(requested) == 0 {
		out := make([]string, len(defaults))
		copy(out, defaults)
		return out
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(requested))
	for _, cap := range requested {
		if _, ok := allowed[cap]; !ok {
			continue
		}
		if _, exists := seen[cap]; exists {
			continue
		}
		seen[cap] = struct{}{}
		out = append(out, cap)
	}
	if len(out) == 0 {
		return sanitizeCapabilities(nil, allowed, defaults)
	}
	return out
}

func validateCapabilities(requested []string, allowed map[string]struct{}) error {
	for _, cap := range requested {
		if _, ok := allowed[cap]; !ok {
			return fmt.Errorf("invalid capability: %s", cap)
		}
	}
	return nil
}
