package serverconfig

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"filippo.io/edwards25519"
)

const (
	SnellObfsNone      = "none"
	SnellObfsShadowTLS = "shadow-tls"
)

func isSnellProtocol(protocol string) bool {
	return protocol == ProtocolSnell || protocol == ProtocolSnellShadowTLS
}

func validateSnellPresetIdentity(input Input) error {
	if input.SnellVersion != 0 && input.SnellVersion != 5 {
		return errors.New("Snell 预设固定使用 v5；旧版本请通过完整源码管理")
	}
	if input.Protocol == ProtocolSnell && input.SnellObfsMode != "" && input.SnellObfsMode != SnellObfsNone {
		return errors.New("Snell 预设不启用额外承载；请改用 Snell + ShadowTLS v3 预设")
	}
	if input.Protocol == ProtocolSnellShadowTLS {
		if input.SnellObfsMode != "" && input.SnellObfsMode != SnellObfsShadowTLS {
			return errors.New("Snell + ShadowTLS v3 预设固定使用 ShadowTLS")
		}
		if input.SnellShadowTLSVersion != 0 && input.SnellShadowTLSVersion != 3 {
			return errors.New("Snell + ShadowTLS v3 预设固定使用 ShadowTLS v3")
		}
	}
	return nil
}

func normalizeSnellInput(input *Input) {
	input.SnellVersion = 5
	if input.SnellObfsMode == "" {
		input.SnellObfsMode = SnellObfsNone
	}
	if input.Protocol == ProtocolSnell {
		input.SnellObfsMode = SnellObfsNone
	}
	if input.Protocol == ProtocolSnellShadowTLS {
		input.SnellObfsMode = SnellObfsShadowTLS
		input.SnellShadowTLSVersion = 3
	}
	if input.SnellClientFingerprint == "" {
		input.SnellClientFingerprint = "chrome"
	}
}

func normalizeSudokuInput(input *Input) {
	if input.SudokuTableType == "" {
		input.SudokuTableType = "prefer_entropy"
	}
	if input.SudokuHandshakeTimeout == 0 {
		input.SudokuHandshakeTimeout = 5
	}
	if input.SudokuHTTPMaskMode == "" {
		input.SudokuHTTPMaskMode = "ws"
	}
	if input.SudokuMultiplex == "" {
		input.SudokuMultiplex = "off"
	}
}

type clientMetadata struct {
	Version                int    `json:"version"`
	Protocol               string `json:"protocol"`
	SnellReuse             bool   `json:"snell_reuse,omitempty"`
	SnellObfsHost          string `json:"snell_obfs_host,omitempty"`
	SnellClientFingerprint string `json:"snell_client_fingerprint,omitempty"`
	SnellShadowTLSALPN     string `json:"snell_shadow_tls_alpn,omitempty"`
	SudokuClientKey        string `json:"sudoku_client_key,omitempty"`
	SudokuHTTPMaskMode     string `json:"sudoku_httpmask_mode,omitempty"`
	SudokuHTTPMaskTLS      bool   `json:"sudoku_httpmask_tls,omitempty"`
	SudokuHTTPMaskHost     string `json:"sudoku_httpmask_host,omitempty"`
	SudokuMultiplex        string `json:"sudoku_multiplex,omitempty"`
}

// MarshalClientMetadata extracts values that are required to build a client
// profile but either must not be written to the server configuration (for
// example a Sudoku private key) or have no server-side equivalent.
func MarshalClientMetadata(input Input) (string, error) {
	if !isSnellProtocol(input.Protocol) && input.Protocol != ProtocolSudoku {
		return "", nil
	}
	if isSnellProtocol(input.Protocol) {
		normalizeSnellInput(&input)
	} else {
		normalizeSudokuInput(&input)
	}
	metadata := clientMetadata{
		Version: 1, Protocol: input.Protocol,
		SnellReuse: input.SnellReuse, SnellObfsHost: input.SnellObfsHost,
		SnellClientFingerprint: input.SnellClientFingerprint,
		SnellShadowTLSALPN:     input.SnellShadowTLSALPN,
		SudokuClientKey:        input.SudokuClientKey, SudokuHTTPMaskMode: input.SudokuHTTPMaskMode,
		SudokuHTTPMaskTLS: input.SudokuHTTPMaskTLS, SudokuHTTPMaskHost: input.SudokuHTTPMaskHost,
		SudokuMultiplex: input.SudokuMultiplex,
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// ApplyClientMetadata hydrates parsed server configuration with its separately
// protected client-only values. Metadata is scoped by configuration version
// and inbound tag by the store.
func ApplyClientMetadata(input *Input, encoded string) error {
	if input == nil || strings.TrimSpace(encoded) == "" {
		return nil
	}
	var metadata clientMetadata
	if err := json.Unmarshal([]byte(encoded), &metadata); err != nil {
		return fmt.Errorf("decode client metadata: %w", err)
	}
	if metadata.Version != 1 {
		return errors.New("unsupported client metadata version")
	}
	// Full-source edits can remove an inbound and later reuse its tag for a
	// different protocol while metadata is copied forward with the revision.
	// Ignore that stale entry: applying it would be incorrect, but it must not
	// make the otherwise valid configuration workspace unavailable.
	if metadata.Protocol != input.Protocol {
		return nil
	}
	input.SnellReuse = metadata.SnellReuse
	input.SnellObfsHost = metadata.SnellObfsHost
	input.SnellClientFingerprint = metadata.SnellClientFingerprint
	input.SnellShadowTLSALPN = metadata.SnellShadowTLSALPN
	input.SudokuClientKey = metadata.SudokuClientKey
	input.SudokuHTTPMaskMode = metadata.SudokuHTTPMaskMode
	input.SudokuHTTPMaskTLS = metadata.SudokuHTTPMaskTLS
	input.SudokuHTTPMaskHost = metadata.SudokuHTTPMaskHost
	input.SudokuMultiplex = metadata.SudokuMultiplex
	return nil
}

func newSudokuKeyPair() (privateKey, publicKey string, err error) {
	var masterSeed [64]byte
	if _, err = rand.Read(masterSeed[:]); err != nil {
		return "", "", err
	}
	master, err := edwards25519.NewScalar().SetUniformBytes(masterSeed[:])
	if err != nil {
		return "", "", err
	}
	var splitSeed [64]byte
	if _, err = rand.Read(splitSeed[:]); err != nil {
		return "", "", err
	}
	left, err := edwards25519.NewScalar().SetUniformBytes(splitSeed[:])
	if err != nil {
		return "", "", err
	}
	right := new(edwards25519.Scalar).Subtract(master, left)
	privateBytes := append(append(make([]byte, 0, 64), left.Bytes()...), right.Bytes()...)
	public := new(edwards25519.Point).ScalarBaseMult(master)
	return hex.EncodeToString(privateBytes), hex.EncodeToString(public.Bytes()), nil
}

func sudokuPublicKeyFromPrivate(privateKey string) (string, error) {
	decoded, err := hex.DecodeString(strings.TrimSpace(privateKey))
	if err != nil || len(decoded) != 64 {
		return "", errors.New("Sudoku 客户端私钥必须是 64 字节十六进制 split key")
	}
	left, err := edwards25519.NewScalar().SetCanonicalBytes(decoded[:32])
	if err != nil {
		return "", errors.New("Sudoku 客户端私钥左半部分不是有效标量")
	}
	right, err := edwards25519.NewScalar().SetCanonicalBytes(decoded[32:])
	if err != nil {
		return "", errors.New("Sudoku 客户端私钥右半部分不是有效标量")
	}
	master := new(edwards25519.Scalar).Add(left, right)
	return hex.EncodeToString(new(edwards25519.Point).ScalarBaseMult(master).Bytes()), nil
}

func validateListenerOptions(input Input) error {
	if input.ListenerRoutingMark < 0 {
		return errors.New("routing-mark 不能为负数")
	}
	for label, value := range map[string]string{"listener rule": input.ListenerRule, "listener proxy": input.ListenerProxy} {
		if value != "" && !tagPattern.MatchString(value) {
			return fmt.Errorf("%s 只能包含字母、数字、点、下划线和短横线，最长 64 位", label)
		}
	}
	return nil
}

func validateSnellInput(input Input) error {
	if input.SnellVersion != 5 {
		return errors.New("Snell 预设固定使用 v5")
	}
	if input.SnellObfsMode == "" {
		input.SnellObfsMode = SnellObfsNone
	}
	switch input.SnellObfsMode {
	case SnellObfsNone:
		return nil
	case SnellObfsShadowTLS:
		if err := validatePresetHost("ShadowTLS Host", input.SnellObfsHost); err != nil {
			return err
		}
		if input.SnellShadowTLSVersion < 1 || input.SnellShadowTLSVersion > 3 {
			return errors.New("ShadowTLS 版本必须是 v1、v2 或 v3")
		}
		if _, _, ok := splitForwardTarget(input.SnellShadowTLSHandshake); !ok {
			return errors.New("ShadowTLS 握手目标必须是有效的 host:port")
		}
		if input.SnellShadowTLSVersion >= 2 && len(input.SnellShadowTLSPassword) < 8 {
			return errors.New("ShadowTLS v2/v3 密码至少需要 8 个字符")
		}
		if input.SnellShadowTLSVersion == 3 && strings.TrimSpace(input.SnellShadowTLSUser) == "" {
			return errors.New("ShadowTLS v3 必须填写用户名")
		}
		_, err := validatedALPN(input.SnellShadowTLSALPN)
		return err
	default:
		return errors.New("不支持的 Snell 伪装模式")
	}
}

func validateSudokuInput(input Input) error {
	publicBytes, err := hex.DecodeString(input.Credential)
	if err != nil || len(publicBytes) != 32 {
		return errors.New("Sudoku 服务端必须使用上游生成的 32 字节十六进制 Master Public Key")
	}
	if _, err := new(edwards25519.Point).SetBytes(publicBytes); err != nil {
		return errors.New("Sudoku 服务端 Master Public Key 不是有效 Ed25519 点")
	}
	derived, err := sudokuPublicKeyFromPrivate(input.SudokuClientKey)
	if err != nil {
		return err
	}
	if derived != strings.ToLower(input.Credential) {
		return errors.New("Sudoku Master Public Key 与客户端 Available Private Key 不属于同一密钥对")
	}
	if input.Method != "chacha20-poly1305" && input.Method != "aes-128-gcm" {
		return errors.New("不支持的 Sudoku AEAD 方法")
	}
	if input.SudokuPaddingMin < 0 || input.SudokuPaddingMin > 100 || input.SudokuPaddingMax < input.SudokuPaddingMin || input.SudokuPaddingMax > 100 {
		return errors.New("Sudoku Padding 必须在 0 到 100 之间，且最大值不能小于最小值")
	}
	if input.SudokuTableType == "" {
		input.SudokuTableType = "prefer_entropy"
	}
	switch input.SudokuTableType {
	case "prefer_ascii", "prefer_entropy", "up_ascii_down_entropy", "up_entropy_down_ascii":
	default:
		return errors.New("不支持的 Sudoku Table Type")
	}
	if input.SudokuHandshakeTimeout < 1 || input.SudokuHandshakeTimeout > 300 {
		return errors.New("Sudoku 握手超时必须在 1 到 300 秒之间")
	}
	if input.SudokuHTTPMaskMode == "" {
		input.SudokuHTTPMaskMode = "ws"
	}
	switch input.SudokuHTTPMaskMode {
	case "stream", "poll", "auto", "ws":
	default:
		return errors.New("Sudoku 预设仅支持已通过真实流量验证的 stream、poll、auto 或 ws HTTPMask 模式")
	}
	if input.SudokuHTTPMaskEnabled {
		if input.SudokuHTTPMaskHost != "" {
			if err := validateHostWithOptionalPort("HTTPMask Host", input.SudokuHTTPMaskHost); err != nil {
				return err
			}
		}
		if err := validatePathRoot(input.SudokuHTTPMaskPathRoot); err != nil {
			return err
		}
	}
	if input.SudokuFallback != "" {
		if _, _, ok := splitForwardTarget(input.SudokuFallback); !ok {
			return errors.New("Sudoku fallback 必须是有效的 host:port")
		}
	}
	if input.SudokuMultiplex == "" {
		input.SudokuMultiplex = "off"
	}
	if input.SudokuMultiplex != "off" && input.SudokuMultiplex != "auto" && input.SudokuMultiplex != "on" {
		return errors.New("Sudoku multiplex 必须是 off、auto 或 on")
	}
	return nil
}

func validatePresetHost(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s 不能为空", label)
	}
	if _, err := NormalizeClientAddress(value); err != nil {
		return fmt.Errorf("%s 必须是有效域名或 IP 地址", label)
	}
	return nil
}

func validateHostWithOptionalPort(label, value string) error {
	if host, port, err := net.SplitHostPort(value); err == nil {
		if _, normalizeErr := NormalizeClientAddress(host); normalizeErr != nil {
			return fmt.Errorf("%s 主机无效", label)
		}
		parsedPort, parseErr := strconv.Atoi(port)
		if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return fmt.Errorf("%s 端口无效", label)
		}
		return nil
	}
	return validatePresetHost(label, value)
}

func validatedALPN(value string) ([]string, error) {
	items := splitPresetList(value)
	for _, item := range items {
		if item != "h2" && item != "http/1.1" {
			return nil, fmt.Errorf("ALPN %q 不受支持；仅允许 h2 和 http/1.1", item)
		}
	}
	return items, nil
}

func splitPresetList(value string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, item := range strings.FieldsFunc(value, func(character rune) bool {
		return character == ',' || character == '\n' || character == '\r'
	}) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func validatePathRoot(value string) error {
	if len(value) > 128 {
		return errors.New("HTTPMask Path Root 最长 128 位")
	}
	segment := strings.Trim(strings.TrimSpace(value), "/")
	if segment == "" && strings.TrimSpace(value) != "" {
		return errors.New("HTTPMask Path Root 必须包含一个有效的一级路径名")
	}
	if strings.Contains(segment, "/") {
		return errors.New("HTTPMask Path Root 只能是一个一级路径名")
	}
	for _, character := range segment {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return errors.New("HTTPMask Path Root 只能包含字母、数字、下划线和短横线")
	}
	return nil
}
