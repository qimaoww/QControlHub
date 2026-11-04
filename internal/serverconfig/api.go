package serverconfig

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// DisableXrayMutableAPI requires explicit consent at the mutation boundary.
// Retain the transport, routes and every known non-routing service. A reserved
// tag or an old revision alone never authorizes removal of a user's service.
func DisableXrayMutableAPI(engine core.Engine, content string) (string, error) {
	if engine != core.EngineXray {
		return "", fmt.Errorf("API service migration is only supported for Xray")
	}
	root, err := decodeAccountingRoot(engine, content)
	if err != nil {
		return "", err
	}
	if _, err := xrayAccountingAPIAddress(root); err != nil {
		return "", err
	}
	api := mapValue(root["api"])
	if api == nil {
		return content, nil
	}
	services, ok := api["services"].([]any)
	if !ok {
		return "", fmt.Errorf("Xray api.services must be an array")
	}
	kept := []any{}
	for _, raw := range services {
		name, ok := raw.(string)
		if ok && (strings.EqualFold(name, "HandlerService") || strings.EqualFold(name, "RoutingService")) {
			continue
		}
		kept = append(kept, raw)
	}
	api["services"] = kept
	if err := rejectMutableRoutingAPI(engine, root); err != nil {
		return "", err
	}
	encoded, err := json.MarshalIndent(root, "", "  ")
	return string(encoded) + "\n", err
}

func xrayAccountingAPIAddress(root map[string]any) (string, error) {
	api := mapValue(root["api"])
	if api == nil {
		return "127.0.0.1:10085", nil
	}
	if address := stringValue(api["listen"]); address != "" {
		host, port, err := net.SplitHostPort(address)
		n, _ := strconv.Atoi(port)
		if err != nil || (host != "127.0.0.1" && host != "::1") || n < 1 || n > 65535 {
			return "", fmt.Errorf("Xray statistics API must use a loopback TCP address")
		}
		return address, nil
	}
	inbounds, _ := root["inbounds"].([]any)
	for _, raw := range inbounds {
		in := mapValue(raw)
		if !xrayInternalAPIInbound(root, in) {
			continue
		}
		host := stringValue(in["listen"])
		port := trafficPortNumber(in["port"])
		if host != "127.0.0.1" && host != "::1" {
			continue
		}
		network := stringValue(mapValue(in["settings"])["network"])
		// The statistics client speaks plaintext gRPC over TCP.
		if port == 0 || (network != "" && network != "tcp") || in["streamSettings"] != nil {
			continue
		}
		return net.JoinHostPort(host, strconv.Itoa(port)), nil
	}
	return "", fmt.Errorf("existing Xray API has no verified plaintext loopback TCP inbound")
}

// Recognize an API transport by its declared API target and an unconditional
// inbound route, never by an arbitrary listener name alone.
func xrayInternalAPIInbound(root, inbound map[string]any) bool {
	apiTag := stringValue(mapValue(root["api"])["tag"])
	tag := stringValue(inbound["tag"])
	if apiTag == "" || tag == "" || stringValue(inbound["protocol"]) != "dokodemo-door" {
		return false
	}
	rules, _ := mapValue(root["routing"])["rules"].([]any)
	for _, raw := range rules {
		rule := mapValue(raw)
		if !accountingRuleMatches(rule["inboundTag"], tag) {
			continue
		}
		if stringValue(rule["outboundTag"]) != apiTag {
			return false
		}
		for key := range rule {
			if key != "type" && key != "inboundTag" && key != "outboundTag" {
				return false
			}
		}
		return rule["inboundTag"] != nil
	}
	return false
}
