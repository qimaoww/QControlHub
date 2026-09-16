package serverconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type clientMetadata struct {
	Version                   int    `json:"version"`
	Protocol                  string `json:"protocol"`
	SnellReuse                bool   `json:"snell_reuse,omitempty"`
	SnellObfsHost             string `json:"snell_obfs_host,omitempty"`
	SnellClientFingerprint    string `json:"snell_client_fingerprint,omitempty"`
	SnellShadowTLSALPN        string `json:"snell_shadow_tls_alpn,omitempty"`
	SudokuClientKey           string `json:"sudoku_client_key,omitempty"`
	SudokuHTTPMaskMode        string `json:"sudoku_httpmask_mode,omitempty"`
	SudokuHTTPMaskTLS         bool   `json:"sudoku_httpmask_tls,omitempty"`
	SudokuHTTPMaskHost        string `json:"sudoku_httpmask_host,omitempty"`
	SudokuMultiplex           string `json:"sudoku_multiplex,omitempty"`
	WireGuardClientPrivateKey string `json:"wireguard_client_private_key,omitempty"`
	WireGuardClientPublicKey  string `json:"wireguard_client_public_key,omitempty"`
	WireGuardAllowedIPs       string `json:"wireguard_allowed_ips,omitempty"`
	WireGuardKeepalive        *int   `json:"wireguard_keepalive,omitempty"`
}

// MarshalClientMetadata extracts client-only values for separately encrypted,
// versioned storage. Never copy server private material or unrelated protocols.
func MarshalClientMetadata(input Input) (string, error) {
	metadata := clientMetadata{Version: 1, Protocol: input.Protocol}
	switch input.Protocol {
	case ProtocolSnell, ProtocolSnellShadowTLS:
		normalizeSnellInput(&input)
		metadata.SnellReuse, metadata.SnellObfsHost = input.SnellReuse, input.SnellObfsHost
		metadata.SnellClientFingerprint, metadata.SnellShadowTLSALPN = input.SnellClientFingerprint, input.SnellShadowTLSALPN
	case ProtocolSudoku:
		normalizeSudokuInput(&input)
		metadata.SudokuClientKey, metadata.SudokuHTTPMaskMode = input.SudokuClientKey, input.SudokuHTTPMaskMode
		metadata.SudokuHTTPMaskTLS, metadata.SudokuHTTPMaskHost = input.SudokuHTTPMaskTLS, input.SudokuHTTPMaskHost
		metadata.SudokuMultiplex = input.SudokuMultiplex
	case ProtocolWireGuard:
		metadata.WireGuardClientPrivateKey, metadata.WireGuardClientPublicKey = input.WireGuardClientPrivateKey, input.WireGuardClientPublicKey
		metadata.WireGuardAllowedIPs, metadata.WireGuardKeepalive = input.WireGuardAllowedIPs, &input.WireGuardKeepalive
	default:
		return "", nil
	}
	encoded, err := json.Marshal(metadata)
	return string(encoded), err
}

// ApplyClientMetadata hydrates parsed source with client-only values. The
// store scopes metadata to a configuration version and inbound tag.
func ApplyClientMetadata(input *Input, encoded string) error {
	if input == nil || strings.TrimSpace(encoded) == "" {
		return nil
	}
	var metadata clientMetadata
	if err := json.Unmarshal([]byte(encoded), &metadata); err != nil {
		return fmt.Errorf("decode client metadata: %w", err)
	}
	if metadata.Version != 1 {
		return errors.New("unsupported client metadata version")
	}
	// A source edit can reuse a tag for another protocol or rotate a peer.
	// Stale metadata must neither overwrite source nor break the workspace.
	if metadata.Protocol != input.Protocol {
		return nil
	}
	switch input.Protocol {
	case ProtocolSnell, ProtocolSnellShadowTLS:
		input.SnellReuse, input.SnellObfsHost = metadata.SnellReuse, metadata.SnellObfsHost
		input.SnellClientFingerprint, input.SnellShadowTLSALPN = metadata.SnellClientFingerprint, metadata.SnellShadowTLSALPN
	case ProtocolSudoku:
		input.SudokuClientKey, input.SudokuHTTPMaskMode = metadata.SudokuClientKey, metadata.SudokuHTTPMaskMode
		input.SudokuHTTPMaskTLS, input.SudokuHTTPMaskHost = metadata.SudokuHTTPMaskTLS, metadata.SudokuHTTPMaskHost
		input.SudokuMultiplex = metadata.SudokuMultiplex
	case ProtocolWireGuard:
		if metadata.WireGuardClientPublicKey != input.WireGuardClientPublicKey ||
			wireguardServerPublic(metadata.WireGuardClientPrivateKey) != input.WireGuardClientPublicKey {
			return nil
		}
		// Source remains authoritative for public keys, PSK, addresses and MTU.
		input.WireGuardClientPrivateKey = metadata.WireGuardClientPrivateKey
		input.WireGuardAllowedIPs = metadata.WireGuardAllowedIPs
		if metadata.WireGuardKeepalive != nil {
			input.WireGuardKeepalive = *metadata.WireGuardKeepalive
		}
	}
	return nil
}
