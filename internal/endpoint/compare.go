package endpoint

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// resolveAllEndpoints resolves an endpoint to all IP:Port combinations.
// If the host is an IP address, returns a single result.
// If the host is a domain, queries both A and AAAA records.
func resolveAllEndpoints(endpoint string) ([]string, error) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint %q: %w", endpoint, err)
	}

	if ip := net.ParseIP(host); ip != nil {
		return []string{normalizeIPPort(ip.String(), port)}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS lookup %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no DNS results for %q", host)
	}

	results := make([]string, 0, len(ips))
	for _, ipAddr := range ips {
		results = append(results, normalizeIPPort(ipAddr.IP.String(), port))
	}
	return results, nil
}

// endpointsMatch compares the user-configured endpoint with the actual WG endpoint.
// The user endpoint is resolved to all A/AAAA records; any match passes.
func endpointsMatch(userEndpoint, actualEndpoint string) (bool, error) {
	allowedEndpoints, err := resolveAllEndpoints(userEndpoint)
	if err != nil {
		return false, err
	}

	resolvedActual, err := resolveEndpoint(actualEndpoint)
	if err != nil {
		return false, err
	}

	for _, allowed := range allowedEndpoints {
		if allowed == resolvedActual {
			return true, nil
		}
	}
	return false, nil
}

// resolveEndpoint resolves a single endpoint to IP:Port.
func resolveEndpoint(endpoint string) (string, error) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint %q: %w", endpoint, err)
	}

	if ip := net.ParseIP(host); ip != nil {
		return normalizeIPPort(ip.String(), port), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", fmt.Errorf("DNS lookup %q: %w", host, err)
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("no DNS results for %q", host)
	}
	return normalizeIPPort(ips[0].IP.String(), port), nil
}

// normalizeIPPort normalizes an IP:Port string for comparison.
func normalizeIPPort(ip, port string) string {
	ip = strings.TrimPrefix(ip, "[")
	ip = strings.TrimSuffix(ip, "]")
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return net.JoinHostPort(ip, port)
	}
	return net.JoinHostPort(parsedIP.String(), port)
}
