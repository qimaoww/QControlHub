package serverconfig

import (
	"errors"
	"net"
	"strings"
)

// NormalizeClientAddress validates and canonicalizes the address that will be
// embedded in a client connection profile. Wildcard and loopback listeners
// are deliberately rejected because they are not reachable client targets.
func NormalizeClientAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	}
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "/@?#%") {
		return "", errors.New("客户端连接地址必须是有效域名或 IP 地址")
	}
	if ip := net.ParseIP(value); ip != nil {
		if ip.IsUnspecified() {
			return "", errors.New("客户端连接地址不能使用 0.0.0.0 或 :: 等监听地址")
		}
		return ip.String(), nil
	}
	value = strings.TrimSuffix(strings.ToLower(value), ".")
	if value == "" {
		return "", errors.New("客户端连接地址必须是有效域名或 IP 地址")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("客户端连接地址必须是有效域名或 IP 地址")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", errors.New("客户端连接地址必须是有效域名或 IP 地址")
			}
		}
	}
	return value, nil
}
