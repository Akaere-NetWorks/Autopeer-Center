// Package peering centralizes the rules for turning a user-supplied peering
// request into a peer record: WireGuard interface naming, listen-port
// allocation, and field validation. All creation entry points (HTTP, MCP, and
// the Telegram bot) MUST go through here so they cannot drift apart — a past
// bug had the bot computing a different interface name and port than the HTTP
// path, producing peers the agent could never configure.
package peering

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"regexp"
	"strconv"

	"github.com/akaere/autopeer-center/internal/model"
)

// DefaultWgPortPrefix is used when a node does not set a custom WgPortPrefix.
const DefaultWgPortPrefix = 5

var (
	base64Re   = regexp.MustCompile(`^[A-Za-z0-9+/]+=*$`)
	hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`)
)

// WgInterfaceName returns the canonical WireGuard interface name for an ASN.
// This MUST match the agent's wg.InterfaceName (dn42<ASN>); the agent falls back
// to that name whenever the center sends an empty interface, and ScanPeers/import
// parse it back out, so any other format breaks reconciliation.
func WgInterfaceName(asn int64) string {
	return fmt.Sprintf("dn42%d", asn)
}

// WgPort returns the WireGuard listen port for a peer on a node with the given
// per-node port prefix. A zero prefix falls back to DefaultWgPortPrefix.
func WgPort(asn int64, portPrefix int) int {
	if portPrefix == 0 {
		portPrefix = DefaultWgPortPrefix
	}
	return portPrefix*10000 + int(asn%10000)
}

// ValidationError signals that a peer input failed validation. Callers map its
// Message into their own response format (HTTP 400, MCP error, bot result).
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(msg string) error { return &ValidationError{Message: msg} }

// ValidatePubkey checks a base64 WireGuard public key.
func ValidatePubkey(pubkey string) error {
	if !base64Re.MatchString(pubkey) || len(pubkey) < 40 {
		return invalid("Invalid WireGuard public key (must be Base64)")
	}
	return nil
}

// ValidateEndpoint checks an "IP:Port" / "[IPv6]:Port" / "host:Port" endpoint.
func ValidateEndpoint(ep string) error {
	host, portStr, err := net.SplitHostPort(ep)
	if err != nil {
		return invalid("Invalid endpoint (format: IP:Port or [IPv6]:Port)")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return invalid("Invalid endpoint (format: IP:Port or [IPv6]:Port)")
	}
	if net.ParseIP(host) == nil && !hostnameRe.MatchString(host) {
		return invalid("Invalid endpoint (format: IP:Port or [IPv6]:Port)")
	}
	return nil
}

// ValidateLLA checks a link-local (fe80::/10) address.
func ValidateLLA(lla string) error {
	ip := net.ParseIP(lla)
	if ip == nil || !ip.IsLinkLocalUnicast() {
		return invalid("Invalid link-local address (must be fe80::/10)")
	}
	return nil
}

// ValidateMTU checks an optional MTU value.
func ValidateMTU(mtu *int) error {
	if mtu != nil && (*mtu < 576 || *mtu > 9000) {
		return invalid("MTU must be between 576 and 9000")
	}
	return nil
}

// GeneratePSK generates a fresh WireGuard pre-shared key: 32 random bytes
// encoded as standard base64 (the same format `wg genpsk` produces).
func GeneratePSK() (string, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return "", fmt.Errorf("generate preshared key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key[:]), nil
}

// Input is the user-supplied portion of a peer creation request.
type Input struct {
	ASN        int64
	NodeID     string
	PortPrefix int
	Pubkey     string
	Endpoint   string
	LLA        string
	MTU        *int
	// EnablePSK requests that the center generate a WireGuard pre-shared key
	// for this peer. The key is symmetric: the same value is pushed to the
	// agent and returned to the owner so they can configure their own end.
	EnablePSK bool
}

// BuildPendingPeer validates the input and constructs a pending, managed peer
// with the canonical interface name and port already filled in. The caller is
// responsible for ContactEmail and for persisting the record. It returns a
// *ValidationError when any field is invalid.
func BuildPendingPeer(in Input) (*model.Peer, error) {
	if in.NodeID == "" {
		return nil, invalid("node_id is required")
	}
	if err := ValidatePubkey(in.Pubkey); err != nil {
		return nil, err
	}
	if err := ValidateEndpoint(in.Endpoint); err != nil {
		return nil, err
	}
	if err := ValidateLLA(in.LLA); err != nil {
		return nil, err
	}
	if err := ValidateMTU(in.MTU); err != nil {
		return nil, err
	}
	var psk *string
	if in.EnablePSK {
		key, err := GeneratePSK()
		if err != nil {
			return nil, err
		}
		psk = &key
	}
	return &model.Peer{
		NodeID:          in.NodeID,
		RemoteASN:       in.ASN,
		RemotePubkey:    in.Pubkey,
		RemoteEndpoint:  in.Endpoint,
		RemoteLLA:       in.LLA,
		WgListenPort:    WgPort(in.ASN, in.PortPrefix),
		WgInterfaceName: WgInterfaceName(in.ASN),
		MTU:             in.MTU,
		WgPreSharedKey:  psk,
		WgManaged:       true,
		Status:          "pending",
	}, nil
}
