package serverconfig

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
)

var ErrInvalidPlanInput = errors.New("invalid server plan input")

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

// RegeneratePlan creates fresh random plan values while retaining the current
// operator-selected settings that are not themselves generated parameters.
func RegeneratePlan(protocol Protocol, current Input) (Input, error) {
	plan, err := NewPlan(protocol)
	if err != nil {
		return Input{}, err
	}

	if len(protocol.Methods) > 0 {
		method := strings.TrimSpace(current.Method)
		if method == "" {
			method = plan.Method
		}
		if !slices.Contains(protocol.Methods, method) {
			return Input{}, fmt.Errorf("%w: unsupported method %q for %s", ErrInvalidPlanInput, method, protocol.Key)
		}
		plan.Method = method
		plan.Credential, err = NewCredential(protocol.Key, method)
		if err != nil {
			return Input{}, err
		}
	}

	plan.Listen = current.Listen
	if protocol.IgnoresUsername {
		plan.Username = current.Username
	}
	randomTransportPath := plan.TransportPath
	plan.Transport = current.Transport
	plan.TransportPath = current.TransportPath
	if current.Transport == "websocket" || current.Transport == "grpc" {
		pathSuffix := strings.TrimPrefix(randomTransportPath, "/")
		if pathSuffix == "" {
			pathSuffix = strings.TrimPrefix(plan.Tag, protocol.Key+"-")
		}
		if current.Transport == "websocket" {
			plan.TransportPath = "/" + pathSuffix
		} else {
			plan.TransportPath = pathSuffix
		}
	}
	plan.TLSEnabled = current.TLSEnabled
	plan.CertificatePath = current.CertificatePath
	plan.PrivateKeyPath = current.PrivateKeyPath
	plan.RealityEnabled = current.RealityEnabled
	plan.RealityServerName = current.RealityServerName
	return plan, nil
}
