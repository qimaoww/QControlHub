package agent

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"go.yaml.in/yaml/v4"
)

func validateMihomoCapabilities(ctx context.Context, binary, content string) error {
	if err := validateMihomoSnellCapabilities(ctx, binary, content); err != nil {
		return err
	}
	return validateMihomoXHTTPCapabilities(ctx, binary, content)
}

// Inspect native input too: an opaque listener or outbound must not bypass the
// version gate. Older binaries can accept unknown YAML fields without effect.
func validateMihomoSnellCapabilities(ctx context.Context, binary, content string) error {
	var root struct {
		Listeners []struct {
			Type    string `yaml:"type"`
			Version int    `yaml:"version"`
		} `yaml:"listeners"`
		Proxies []struct {
			Type    string `yaml:"type"`
			Version int    `yaml:"version"`
		} `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return err
	}
	needsV6 := false
	for _, listener := range root.Listeners {
		needsV6 = needsV6 || listener.Type == "snell" && listener.Version == 6
	}
	for _, proxy := range root.Proxies {
		needsV6 = needsV6 || proxy.Type == "snell" && proxy.Version == 6
	}
	if !needsV6 {
		return nil
	}
	output, err := run(ctx, binary, "-v")
	if err != nil {
		return fmt.Errorf("Mihomo Snell v6 版本检查失败：%w", err)
	}
	match := regexp.MustCompile(`Mihomo(?: Meta)? v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:\s|$)`).FindStringSubmatch(output)
	if len(match) != 4 {
		return fmt.Errorf("无法确认 Snell v6 支持，请安装 Mihomo 正式版 1.19.34 或更新版本")
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	if major*1000000+minor*1000+patch < 1019034 {
		return fmt.Errorf("Snell v6 需要 Mihomo 1.19.34+，请由有内核管理权限的用户升级后重试")
	}
	return nil
}
