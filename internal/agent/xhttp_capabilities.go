package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"go.yaml.in/yaml/v4"
	"net"
	"os"
	"regexp"
	"strconv"
)

var xrayVersionPattern = regexp.MustCompile(`Xray\s+([0-9]+)\.([0-9]+)\.([0-9]+)`)

func validateXrayXHTTPCapabilities(ctx context.Context, binary, content string) error {
	var root struct {
		Inbounds []struct {
			Stream struct {
				Network string `json:"network"`
			} `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return err
	}
	found := false
	for _, ib := range root.Inbounds {
		found = found || ib.Stream.Network == "xhttp"
	}
	if !found {
		return nil
	}
	output, err := run(ctx, binary, "version")
	if err != nil {
		return fmt.Errorf("XHTTP 内核版本检查：%w", err)
	}
	match := xrayVersionPattern.FindStringSubmatch(output)
	if len(match) != 4 {
		return fmt.Errorf("无法识别 Xray 版本，不能确认 XHTTP 能力；请安装官方稳定版")
	}
	year, _ := strconv.Atoi(match[1])
	month, _ := strconv.Atoi(match[2])
	day, _ := strconv.Atoi(match[3])
	if year*10000+month*100+day < 241215 {
		return fmt.Errorf("XHTTP 预设需要两端 Xray >= 24.12.15，当前 %s；请先升级内核", match[0])
	}
	return nil
}

func checkXHTTPListenerPorts(spec EngineSpec, content string) error {
	return checkXrayListenerPorts(spec, content, false)
}

// Mihomo can remain active after a listener fails. Probe every new listener,
// including UDP and native/global ports, before accepting a deployment.
func checkMihomoListenerPorts(spec EngineSpec, content string, retainActive bool) error {
	type listener struct {
		address  string
		protocol core.TrafficProtocol
	}
	listeners := func(source string) []listener {
		var root map[string]any
		if yaml.Unmarshal([]byte(source), &root) != nil {
			return nil
		}
		transports := map[int]core.TrafficProtocol{}
		for _, endpoint := range serverconfig.DiscoverTrafficPorts(core.EngineMihomo, source) {
			transports[endpoint.Port] = endpoint.Protocol
		}
		var result []listener
		add := func(host string, raw any) {
			if host == "*" {
				host = ""
			}
			port, _ := strconv.Atoi(fmt.Sprint(raw))
			if port > 0 && port <= 65535 {
				result = append(result, listener{net.JoinHostPort(host, strconv.Itoa(port)), transports[port]})
			}
		}
		entries, _ := root["listeners"].([]any)
		for _, raw := range entries {
			entry, _ := raw.(map[string]any)
			host, _ := entry["listen"].(string)
			add(host, entry["port"])
		}
		host, _ := root["bind-address"].(string)
		if host == "*" {
			host = ""
		}
		for _, key := range []string{"port", "socks-port", "mixed-port", "redir-port", "tproxy-port"} {
			add(host, root[key])
		}
		return result
	}
	previous := map[listener]bool{}
	if retainActive {
		if data, err := os.ReadFile(spec.ConfigPath); err == nil {
			for _, entry := range listeners(string(data)) {
				previous[entry] = true
			}
		}
	}
	for _, entry := range listeners(content) {
		if previous[entry] {
			continue
		}
		if entry.protocol != core.TrafficProtocolUDP {
			probe, err := net.Listen("tcp", entry.address)
			if err != nil {
				return fmt.Errorf("Mihomo TCP 监听预检 %s：%w；请检查地址及端口占用", entry.address, err)
			}
			probe.Close()
		}
		if entry.protocol == core.TrafficProtocolUDP || entry.protocol == core.TrafficProtocolBoth {
			probe, err := net.ListenPacket("udp", entry.address)
			if err != nil {
				return fmt.Errorf("Mihomo UDP 监听预检 %s：%w；请检查地址及端口占用", entry.address, err)
			}
			probe.Close()
		}
	}
	return nil
}

func checkXrayListenerPorts(spec EngineSpec, content string, allListeners bool) error {
	type inbound struct {
		Listen string `json:"listen"`
		Port   int    `json:"port"`
		Stream struct {
			Network string `json:"network"`
		} `json:"streamSettings"`
	}
	type document struct {
		Inbounds []inbound `json:"inbounds"`
	}
	var next, previous document
	if err := json.Unmarshal([]byte(content), &next); err != nil {
		return err
	}
	hasXHTTP := false
	for _, ib := range next.Inbounds {
		hasXHTTP = hasXHTTP || ib.Stream.Network == "xhttp"
	}
	if !hasXHTTP && !allListeners {
		return nil
	}
	if data, err := os.ReadFile(spec.ConfigPath); err == nil {
		_ = json.Unmarshal(data, &previous)
	}
	owned := map[string]bool{}
	for _, ib := range previous.Inbounds {
		owned[net.JoinHostPort(ib.Listen, strconv.Itoa(ib.Port))] = true
	}
	for _, ib := range next.Inbounds {
		if ib.Port == 0 {
			continue
		}
		address := net.JoinHostPort(ib.Listen, strconv.Itoa(ib.Port))
		if owned[address] {
			continue
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf("Xray 监听预检 %s：%w；请检查地址是否属于节点、IPv6 支持及端口占用", address, err)
		}
		listener.Close()
	}
	return nil
}

// Require the official server implementation actually exercised by our matrix.
func validateMihomoXHTTPCapabilities(ctx context.Context, binary, content string) error {
	found := false
	for _, input := range serverconfig.ParseAll(core.EngineMihomo, content) {
		found = found || input.Protocol == serverconfig.ProtocolXHTTPTLS || input.Transport == "xhttp" && !input.RealityEnabled && input.VLESSDecryption == ""
	}
	if !found {
		return nil
	}
	output, err := run(ctx, binary, "-v")
	if err != nil {
		return fmt.Errorf("Mihomo XHTTP 服务端版本检查：%w", err)
	}
	match := regexp.MustCompile(`Mihomo(?: Meta)? v?([0-9]+)\.([0-9]+)\.([0-9]+)`).FindStringSubmatch(output)
	if len(match) != 4 {
		return fmt.Errorf("无法确认 Mihomo XHTTP 服务端能力，请安装官方稳定版 >= 1.19.33")
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	if major*1000000+minor*1000+patch < 1019033 {
		return fmt.Errorf("Mihomo XHTTP 服务端需要 >= 1.19.33，请先升级内核")
	}
	return nil
}
