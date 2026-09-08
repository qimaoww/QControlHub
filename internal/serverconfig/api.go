package serverconfig

import (
	"fmt"
	"net"
	"strconv"
)

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
