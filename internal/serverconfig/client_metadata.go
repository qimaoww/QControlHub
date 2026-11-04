package serverconfig

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type clientMetadata struct {
	WebSocket                 *webSocketClientMetadata          `json:"websocket_client,omitempty"`
	NativeTLS                 *core.NodeTLSCertificateReference `json:"native_tls,omitempty"`
	PanelCertificateID        string                            `json:"panel_certificate_id,omitempty"`
	PanelCertificateName      string                            `json:"panel_certificate_name,omitempty"`
	ExternalFrontend          *core.TLSFrontend                 `json:"external_tls_frontend,omitempty"`
	XHTTP                     *xhttpMetadata                    `json:"xhttp,omitempty"`
	TLSALPN                   string                            `json:"managed_tls_alpn,omitempty"`
	TLSAuthorityKey           string                            `json:"tls_authority_key,omitempty"`
	Frontend                  *core.TLSFrontend                 `json:"tls_frontend,omitempty"`
	TLSClientCertificate      string                            `json:"tls_client_certificate,omitempty"`
	Version                   int                               `json:"version"`
	Protocol                  string                            `json:"protocol"`
	SnellReuse                bool                              `json:"snell_reuse,omitempty"`
	SnellObfsHost             string                            `json:"snell_obfs_host,omitempty"`
	SnellClientFingerprint    string                            `json:"snell_client_fingerprint,omitempty"`
	SnellShadowTLSALPN        string                            `json:"snell_shadow_tls_alpn,omitempty"`
	SudokuClientKey           string                            `json:"sudoku_client_key,omitempty"`
	SudokuHTTPMaskMode        string                            `json:"sudoku_httpmask_mode,omitempty"`
	SudokuHTTPMaskTLS         bool                              `json:"sudoku_httpmask_tls,omitempty"`
	SudokuHTTPMaskHost        string                            `json:"sudoku_httpmask_host,omitempty"`
	SudokuMultiplex           string                            `json:"sudoku_multiplex,omitempty"`
	WireGuardClientPrivateKey string                            `json:"wireguard_client_private_key,omitempty"`
	WireGuardClientPublicKey  string                            `json:"wireguard_client_public_key,omitempty"`
	WireGuardAllowedIPs       string                            `json:"wireguard_allowed_ips,omitempty"`
	WireGuardKeepalive        *int                              `json:"wireguard_keepalive,omitempty"`
}

// Xray and Mihomo do not encode a client WS Host in their server listener.
// Bind detached client values to the exact saved endpoint and peer.
type webSocketClientMetadata struct {
	Plain           bool   `json:"plain,omitempty"`
	Tag             string `json:"tag"`
	Listen          string `json:"listen"`
	Path            string `json:"path"`
	PeerHash        string `json:"peer_hash"`
	CertificatePath string `json:"certificate_path"`
	PrivateKeyPath  string `json:"private_key_path"`
	ServerName      string `json:"server_name,omitempty"`
	Host            string `json:"host,omitempty"`
	Port            int    `json:"port"`
}

func webSocketClientIdentity(input Input) webSocketClientMetadata {
	return webSocketClientMetadata{Plain: !input.TLSEnabled, Tag: input.Tag, Listen: input.Listen, Port: input.Port, Path: input.TransportPath,
		PeerHash: fmt.Sprintf("%x", sha256.Sum256([]byte(input.Credential))), CertificatePath: input.CertificatePath, PrivateKeyPath: input.PrivateKeyPath}
}

// MarshalClientMetadata extracts client-only values for separately encrypted,
// versioned storage. Never copy server private material or unrelated protocols.
func MarshalClientMetadata(input Input) (string, error) {
	metadata := clientMetadata{Version: 1, Protocol: input.Protocol}
	if input.Transport == "websocket" && !input.RealityEnabled && !proxiedTLS(input) {
		ws := webSocketClientIdentity(input)
		ws.Host = input.TransportHost
		if input.TLSEnabled {
			ws.ServerName = input.TLSServerName
		}
		metadata.WebSocket = &ws
	}
	if input.PanelCertificateID == "" && input.TLSEnabled && !input.RealityEnabled && (input.TLSMode == "" || input.TLSMode == "native") && input.CertificatePath != "" && input.PrivateKeyPath != "" && input.TLSServerName != "" {
		metadata.NativeTLS = &core.NodeTLSCertificateReference{Tag: input.Tag, Domain: input.TLSServerName, CertificatePath: input.CertificatePath, PrivateKeyPath: input.PrivateKeyPath}
	}
	if input.PanelCertificateID != "" {
		metadata.PanelCertificateID = input.PanelCertificateID
		metadata.PanelCertificateName = input.TLSServerName
	}
	if input.Transport == "xhttp" {
		metadata.XHTTP = marshalXHTTPMetadata(input)
		encoded, err := json.Marshal(metadata)
		return string(encoded), err
	}
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
		_, preset := singBoxPresetFor(input.Protocol)
		if input.TLSClientCertificate == "" && !preset && !proxiedTLS(input) && input.PanelCertificateID == "" && metadata.NativeTLS == nil && metadata.WebSocket == nil {
			return "", nil
		}
		metadata.TLSClientCertificate = input.TLSClientCertificate
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
	if metadata.XHTTP != nil && input.Transport == "xhttp" {
		applyXHTTPMetadata(input, metadata)
		return nil
	}
	if nativeProtocol(metadata.Protocol) != nativeProtocol(input.Protocol) {
		return nil
	}
	f := metadata.Frontend
	if metadata.ExternalFrontend != nil {
		f = metadata.ExternalFrontend
	}
	if f != nil && input.Listen == "127.0.0.1" && input.Port == f.BackendPort && input.Transport == f.Transport && input.TransportPath == f.Path && !input.TLSEnabled && !input.RealityEnabled {
		input.TLSMode, input.TLSPublicPort, input.TLSServerName = f.Mode, f.PublicPort, f.Domain
		input.TLSACMEEmail = f.ACMEEmail
		input.TLSEnabled = true
		input.TLSALPN = metadata.TLSALPN
	}
	if metadata.PanelCertificateID != "" && input.CertificatePath == core.PanelCertificatePrefix+metadata.PanelCertificateID+"/cert.pem" && input.PrivateKeyPath == core.PanelCertificatePrefix+metadata.PanelCertificateID+"/key.pem" {
		input.PanelCertificateID = metadata.PanelCertificateID
		input.TLSServerName = metadata.PanelCertificateName
	}
	if native := metadata.NativeTLS; metadata.WebSocket == nil && native != nil && input.TLSEnabled && !input.RealityEnabled && input.Tag == native.Tag && input.CertificatePath == native.CertificatePath && input.PrivateKeyPath == native.PrivateKeyPath && input.TLSServerName == "" {
		input.TLSServerName = native.Domain
	}
	if input.TLSEnabled {
		input.TLSClientCertificate = metadata.TLSClientCertificate
	}
	if ws := metadata.WebSocket; ws != nil && input.Transport == "websocket" && ws.Plain == !input.TLSEnabled && !input.RealityEnabled && !proxiedTLS(*input) {
		identity := *ws
		identity.ServerName, identity.Host = "", ""
		if identity == webSocketClientIdentity(*input) && (input.TLSServerName == "" || input.TLSServerName == ws.ServerName) {
			if input.TLSServerName == "" {
				input.TLSServerName = ws.ServerName
			}
			if input.TransportHost == "" {
				input.TransportHost = ws.Host
			}
		}
	}
	if preset, ok := singBoxPresetFor(metadata.Protocol); ok && nativeProtocol(input.Protocol) == preset.base && input.Transport == preset.transport && input.TLSEnabled == preset.tls && input.RealityEnabled == preset.reality {
		input.Protocol = metadata.Protocol
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
