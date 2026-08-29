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
	credential := ""
	var err error
	if !protocol.PortForward {
		credential, err = NewCredential(protocol.Key, method)
		if err != nil {
			return Input{}, err
		}
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
	if protocol.Key == ProtocolVLESSXHTTP || protocol.Key == ProtocolVLESSEncXHTTP {
		transport = "xhttp"
		transportPath = "/" + suffix
	}
	input := Input{
		Protocol: protocol.Key, Tag: protocol.Key + "-" + suffix, Listen: "0.0.0.0",
		Port: 20000 + int(portOffset.Int64()), Username: "qch-" + suffix,
		Credential: credential, Method: method, Transport: transport, TransportPath: transportPath,
		TLSEnabled: protocol.DefaultTLS, CertificatePath: "/etc/qcontrolhub/tls/server.crt", PrivateKeyPath: "/etc/qcontrolhub/tls/server.key",
	}
	if protocol.PortForward {
		input.Username = ""
		input.TargetAddress = "127.0.0.1"
		input.TargetPort = 80
		input.Network = "tcp"
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
	if protocol.Key == ProtocolSnell {
		input.SnellVersion = 4
	}
	if protocol.Key == ProtocolSudoku {
		input.SudokuPaddingMin = 1
		input.SudokuPaddingMax = 15
		input.SudokuTableType = "prefer_ascii"
	}
	if protocol.Key == ProtocolVLESS || protocol.Key == ProtocolVLESSXHTTP || isVLESSEncryptionProtocol(protocol.Key) {
		if protocol.Key == ProtocolVLESS || isVLESSEncryptionProtocol(protocol.Key) {
			input.Flow = "xtls-rprx-vision"
		}
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
		input.RealityMinClientVer = "0.0.0"
	}
	if protocol.UsesVLESSEncryption {
		input.VLESSDecryption, input.VLESSEncryption, err = newVLESSEncryptionPair()
		if err != nil {
			return Input{}, err
		}
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
	if current.Transport == "websocket" || current.Transport == "grpc" || current.Transport == "xhttp" {
		pathSuffix := strings.TrimPrefix(randomTransportPath, "/")
		if pathSuffix == "" {
			pathSuffix = strings.TrimPrefix(plan.Tag, protocol.Key+"-")
		}
		if current.Transport == "websocket" || current.Transport == "xhttp" {
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
	plan.RealityMinClientVer = current.RealityMinClientVer
	if protocol.SupportsRealityMLDSA {
		seed := make([]byte, 32)
		if _, err := rand.Read(seed); err != nil {
			return Input{}, err
		}
		plan.RealityMLDSA65Seed = base64.RawURLEncoding.EncodeToString(seed)
		plan.RealityMLDSA65Verify, err = mldsa65VerifyFromSeed(plan.RealityMLDSA65Seed)
		if err != nil {
			return Input{}, err
		}
	}
	plan.SnellVersion = current.SnellVersion
	plan.SudokuPaddingMin = current.SudokuPaddingMin
	plan.SudokuPaddingMax = current.SudokuPaddingMax
	plan.SudokuTableType = current.SudokuTableType
	if protocol.PortForward {
		plan.TargetAddress = current.TargetAddress
		plan.TargetPort = current.TargetPort
		plan.Network = current.Network
	}
	return plan, nil
}
