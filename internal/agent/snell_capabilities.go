package agent

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
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
	type entry struct {
		Type    string `yaml:"type"`
		Version any    `yaml:"version"`
	}
	var root struct {
		Listeners []entry `yaml:"listeners"`
		Proxies   []entry `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return err
	}
	var requirements []string
	for _, listener := range append(root.Listeners, root.Proxies...) {
		if listener.Type == "snell" && listener.Version != nil {
			version, err := strconv.Atoi(fmt.Sprint(listener.Version))
			if err != nil {
				return fmt.Errorf("Snell 协议版本必须为整数")
			}
			if required := serverconfig.SnellMinimumMihomoVersion(version); required != "" {
				requirements = append(requirements, required)
			}
		}
	}
	if len(requirements) == 0 {
		return nil
	}
	output, err := run(ctx, binary, "-v")
	if err != nil {
		return fmt.Errorf("Mihomo Snell v6 版本检查失败：%w", err)
	}
	match := regexp.MustCompile(`Mihomo(?: Meta)? v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:\s|$)`).FindStringSubmatch(output)
	if len(match) != 4 {
		return fmt.Errorf("无法确认 Snell 支持，请安装 Mihomo 正式版 %s 或更新版本", requirements[0])
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	for _, required := range requirements {
		var minimum []int
		for _, part := range strings.Split(required, ".") {
			value, _ := strconv.Atoi(part)
			minimum = append(minimum, value)
		}
		if slices.Compare([]int{major, minor, patch}, minimum) < 0 {
			return fmt.Errorf("Snell 需要 Mihomo %s+，请由有内核管理权限的用户升级后重试", required)
		}
	}
	return nil
}
