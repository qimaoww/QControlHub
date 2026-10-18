package serverconfig

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// escapeClientComponent uses URI component encoding, not form encoding: a
// literal plus must survive clients that decode userinfo or fragments as forms.
func escapeClientComponent(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func clientURL(scheme string, user *url.Userinfo, host string, query url.Values, name string) string {
	u := url.URL{Scheme: scheme, Host: host, RawQuery: strings.ReplaceAll(query.Encode(), "+", "%20"), Fragment: name, RawFragment: escapeClientComponent(name)}
	// URL.User.String deliberately leaves some userinfo characters unescaped.
	// Encode each component independently, keeping only the password separator.
	authority := escapeClientComponent(user.Username())
	if password, present := user.Password(); present {
		authority += ":" + escapeClientComponent(password)
	}
	return strings.Replace(u.String(), "://", "://"+authority+"@", 1)
}

func nativeWebSocketPath(input Input) bool {
	if input.Transport != "websocket" {
		return false
	}
	path, err := url.Parse(input.TransportPath)
	return err == nil && (path.Query().Has("ed") || path.Query().Has("eh"))
}

func nativeClientURLReason(input Input) string {
	if nativeWebSocketPath(input) {
		return "此路径含 WebSocket 分享保留参数，请使用原生配置"
	}
	// Mihomo's Hysteria 2 and SOCKS URI importers use User.String without
	// decoding its escapes. Native maps retain these otherwise valid secrets.
	if input.Protocol == ProtocolHy2 && url.User(input.Credential).String() != input.Credential {
		return "此密码请使用原生配置"
	}
	if input.Protocol == ProtocolSocks && url.UserPassword(input.Username, input.Credential).String() != input.Username+":"+input.Credential {
		return "此认证信息请使用原生配置"
	}
	return ""
}

func vmessURLPayload(value string) (map[string]json.RawMessage, error) {
	encoded := strings.TrimPrefix(strings.SplitN(value, "#", 2)[0], "vmess://")
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, err := encoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(decoded, &payload) == nil && payload["id"] != nil && payload["add"] != nil {
			return payload, nil
		}
	}
	return nil, errors.New("无法解析 VMess 分享链接")
}

// RenameClientURL changes only the display name. VMess stores it inside its
// Base64 JSON; every other supported URL stores it in an escaped fragment.
// Keep the original authority and query bytes when renaming ordinary URLs.
func RenameClientURL(value, name string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("客户端分享链接必须为单行")
	}
	if strings.HasPrefix(value, "vmess://") && !strings.Contains(strings.SplitN(value, "#", 2)[0], "@") {
		payload, err := vmessURLPayload(value)
		if err != nil {
			return "", err
		}
		payload["ps"], _ = json.Marshal(name)
		encoded, err := json.Marshal(payload)
		return "vmess://" + base64.StdEncoding.EncodeToString(encoded), err
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return "", errors.New("无法解析客户端分享链接")
	}
	switch u.Scheme {
	case "ss", "vless", "vmess", "trojan", "hysteria2", "tuic", "socks5", "anytls":
	default:
		return "", errors.New("不支持此客户端分享协议")
	}
	return strings.SplitN(value, "#", 2)[0] + "#" + escapeClientComponent(name), nil
}

// ClientURLName decodes exactly once. In particular '+' is a literal plus in
// an RFC 3986 fragment, and VMess has no fragment in its JSON sharing format.
func ClientURLName(value string) string {
	if strings.HasPrefix(value, "vmess://") && !strings.Contains(strings.SplitN(value, "#", 2)[0], "@") {
		payload, err := vmessURLPayload(value)
		if err != nil {
			return ""
		}
		var name string
		_ = json.Unmarshal(payload["ps"], &name)
		return name
	}
	_, fragment, present := strings.Cut(value, "#")
	if !present {
		return ""
	}
	name, err := url.PathUnescape(fragment)
	if err != nil {
		return ""
	}
	return name
}
