package serverconfig

import (
	"slices"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// A native protocol name is only a parsing hint. The catalog describes concrete
// transport/security combinations, including the flow of the represented user.
// Keep unmatched projections for bound TLS metadata, but never use them as forms
// until that metadata has established a matching preset. Client export reads
// native connection parameters independently of form eligibility.
func matchesParsedPreset(engine core.Engine, input Input) bool {
	if input.Port < 1 || input.Port > 65535 {
		return false
	}
	if isXHTTPGroup(input.Protocol) {
		return engine == core.EngineXray && input.XHTTPBackendPort > 0 && input.XHTTPTLSPort > 0 && input.RealityPublicKey != ""
	}
	protocol, ok := FindProtocol(engine, input.Protocol)
	if !ok || !slices.Contains(protocol.Transports, input.Transport) || input.TLSEnabled && input.RealityEnabled {
		return false
	}
	if protocol.UsesReality != input.RealityEnabled || protocol.RequiresTLS && !input.TLSEnabled || input.TLSEnabled && !protocol.SupportsTLS {
		return false
	}
	if len(protocol.Methods) > 0 && !slices.Contains(protocol.Methods, input.Method) {
		return false
	}
	if input.RealityEnabled && (input.RealityPublicKey == "" || input.RealityServerName == "" || input.RealityTarget == "") {
		return false
	}
	if input.TLSEnabled && !proxiedTLS(input) && (input.CertificatePath == "" || input.PrivateKeyPath == "") {
		return false
	}
	switch input.Protocol {
	case ProtocolVLESS, ProtocolVLESSEncTCP, ProtocolVLESSEncXHTTP:
		return input.Flow == "xtls-rprx-vision"
	case ProtocolVLESSEncPlain, ProtocolVLESSXHTTP, ProtocolXHTTPTLS:
		return input.Flow == ""
	}
	if nativeProtocol(input.Protocol) == ProtocolVLESS {
		return input.Flow == "" || input.Flow == "xtls-rprx-vision" && input.Transport == "raw" && input.TLSEnabled
	}
	return true
}
