package serverconfig

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"math/big"
)

// NewPlan creates a complete randomized server plan. Ports are selected from
// the dynamic/private range; the remote core remains the authority that checks
// whether the chosen port is actually free before deployment.
func NewPlan(protocol Protocol) (Input, error) {
	method := ""
	if len(protocol.Methods) > 0 {
		method = protocol.Methods[0]
	}
	credential, err := NewCredential(protocol.Key, method)
	if err != nil {
		return Input{}, err
	}
	portOffset, err := rand.Int(rand.Reader, big.NewInt(49151-20000+1))
	if err != nil {
		return Input{}, err
	}
	suffixBytes := make([]byte, 4)
	if _, err := rand.Read(suffixBytes); err != nil {
		return Input{}, err
	}
	suffix := hex.EncodeToString(suffixBytes)
	transport := protocol.Transports[0]
	transportPath := ""
	if protocol.Key == ProtocolVMess {
		transport = "websocket"
		transportPath = "/" + suffix
	}
	input := Input{
		Protocol: protocol.Key, Tag: protocol.Key + "-" + suffix, Listen: "0.0.0.0",
		Port: 20000 + int(portOffset.Int64()), Username: "qch-" + suffix,
		Credential: credential, Method: method, Transport: transport, TransportPath: transportPath,
		TLSEnabled: protocol.DefaultTLS, CertificatePath: "/etc/qcontrolhub/tls/server.crt", PrivateKeyPath: "/etc/qcontrolhub/tls/server.key",
	}
	if protocol.Key == ProtocolTUIC {
		input.Credential, err = NewCredential(ProtocolVLESS, "")
		if err != nil {
			return Input{}, err
		}
		input.SecondaryCredential, err = NewCredential(ProtocolTrojan, "")
		if err != nil {
			return Input{}, err
		}
	}
	if protocol.Key == ProtocolVLESS {
		input.Flow = "xtls-rprx-vision"
		privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return Input{}, err
		}
		shortID := make([]byte, 8)
		if _, err := rand.Read(shortID); err != nil {
			return Input{}, err
		}
		input.TLSEnabled = false
		input.CertificatePath, input.PrivateKeyPath = "", ""
		input.RealityEnabled = true
		input.RealityPrivateKey = base64.RawURLEncoding.EncodeToString(privateKey.Bytes())
		input.RealityPublicKey = base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes())
		input.RealityShortID = hex.EncodeToString(shortID)
		input.RealityServerName = DefaultRealityServerName
	}
	return input, nil
}
