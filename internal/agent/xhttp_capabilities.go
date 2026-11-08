package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
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

func checkMihomoXHTTPTLSListenerPorts(spec EngineSpec, content string) error {
	previous := map[string]bool{}
	if data, err := os.ReadFile(spec.ConfigPath); err == nil {
		for _, input := range serverconfig.ParseAll(core.EngineMihomo, string(data)) {
			previous[net.JoinHostPort(input.Listen, strconv.Itoa(input.Port))] = true
		}
	}
	for _, input := range serverconfig.ParseAll(core.EngineMihomo, content) {
		if input.Transport != "xhttp" || input.RealityEnabled || input.VLESSDecryption != "" {
			continue
		}
		address := net.JoinHostPort(input.Listen, strconv.Itoa(input.Port))
		if previous[address] {
			continue
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf("Mihomo XHTTP 监听预检 %s：%w；请检查地址及端口占用", address, err)
		}
		listener.Close()
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
