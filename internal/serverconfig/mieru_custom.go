package serverconfig

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/enfein/mieru/v3/apis/trafficpattern"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"google.golang.org/protobuf/proto"
)

// MieruCustomPattern represents every traffic pattern control exposed by the
// preset editor. All protobuf scalar fields are set explicitly, including 0,
// so Mieru does not generate implicit values for a custom mode.
type MieruCustomPattern struct {
	TCPFragment        bool   `json:"tcp_fragment"`
	MaxSleepMs         int    `json:"max_sleep_ms"`
	NonceType          string `json:"nonce_type"`
	NonceApplyToAllUDP bool   `json:"nonce_apply_to_all_udp"`
	NonceMinLen        int    `json:"nonce_min_len"`
	NonceMaxLen        int    `json:"nonce_max_len"`
	NonceFixedHex      string `json:"nonce_fixed_hex"`
	PaddingMiddle      int    `json:"padding_middle"`
	PaddingEnd         int    `json:"padding_end"`
	LowEntropyMode     int    `json:"low_entropy_mode"`
	MaskRotation       string `json:"mask_rotation"`
}

var mieruNonceTypes = map[string]appctlpb.NonceType{
	"RANDOM":           appctlpb.NonceType_NONCE_TYPE_RANDOM,
	"PRINTABLE":        appctlpb.NonceType_NONCE_TYPE_PRINTABLE,
	"PRINTABLE_SUBSET": appctlpb.NonceType_NONCE_TYPE_PRINTABLE_SUBSET,
	"FIXED":            appctlpb.NonceType_NONCE_TYPE_FIXED,
}

var mieruEntropyModes = map[int]appctlpb.LowEntropyMode{
	0:  appctlpb.LowEntropyMode_LOW_ENTROPY_MODE_OFF,
	32: appctlpb.LowEntropyMode_LOW_ENTROPY_MODE_32,
	40: appctlpb.LowEntropyMode_LOW_ENTROPY_MODE_40,
	48: appctlpb.LowEntropyMode_LOW_ENTROPY_MODE_48,
	56: appctlpb.LowEntropyMode_LOW_ENTROPY_MODE_56,
}

func mieruRotation(value string) (appctlpb.LowEntropyMaskRotation, bool) {
	if value == "none" {
		return appctlpb.LowEntropyMaskRotation_LOW_ENTROPY_MASK_NO_ROTATION, true
	}
	for _, direction := range []struct {
		prefix string
		factor int
	}{{"right_", 1}, {"left_", 16}} {
		if amount, ok := strings.CutPrefix(value, direction.prefix); ok {
			bits, err := strconv.Atoi(amount)
			if err == nil && bits >= 1 && bits <= 15 && amount == strconv.Itoa(bits) {
				return appctlpb.LowEntropyMaskRotation(bits * direction.factor), true
			}
		}
	}
	return 0, false
}

func mieruRotationName(value appctlpb.LowEntropyMaskRotation) (string, bool) {
	if value == appctlpb.LowEntropyMaskRotation_LOW_ENTROPY_MASK_NO_ROTATION {
		return "none", true
	}
	if value >= 1 && value <= 15 {
		return fmt.Sprintf("right_%d", value), true
	}
	if value >= 16 && value <= 240 && value%16 == 0 {
		return fmt.Sprintf("left_%d", value/16), true
	}
	return "", false
}

func mieruFixedHex(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	values := strings.Split(value, ",")
	for i, item := range values {
		item = strings.ToLower(strings.TrimSpace(item))
		bytes, err := hex.DecodeString(item)
		if err != nil || len(bytes) == 0 || len(bytes) > 12 {
			return nil, fmt.Errorf("Mieru 固定 Nonce 第 %d 项必须是 1–12 字节的十六进制字符串", i+1)
		}
		values[i] = item
	}
	return values, nil
}

func validateMieruCustomPattern(custom *MieruCustomPattern) error {
	if custom == nil {
		return fmt.Errorf("Mieru 自定义模式缺少参数")
	}
	if custom.MaxSleepMs < 0 || custom.MaxSleepMs > 100 {
		return fmt.Errorf("Mieru TCP 分片等待必须在 0–100 ms 之间")
	}
	if custom.NonceMinLen < 0 || custom.NonceMaxLen > 12 || custom.NonceMinLen > custom.NonceMaxLen {
		return fmt.Errorf("Mieru Nonce 长度必须在 0–12 字节之间，且最小值不大于最大值")
	}
	if custom.PaddingMiddle < 0 || custom.PaddingMiddle > 255 || custom.PaddingEnd < 0 || custom.PaddingEnd > 255 {
		return fmt.Errorf("Mieru 填充上限必须在 0–255 字节之间")
	}
	if _, ok := mieruNonceTypes[custom.NonceType]; !ok {
		return fmt.Errorf("Mieru Nonce 类型无效")
	}
	if _, ok := mieruEntropyModes[custom.LowEntropyMode]; !ok {
		return fmt.Errorf("Mieru 低熵模式无效")
	}
	if _, ok := mieruRotation(custom.MaskRotation); !ok {
		return fmt.Errorf("Mieru 掩码旋转无效")
	}
	values, err := mieruFixedHex(custom.NonceFixedHex)
	if err != nil {
		return err
	}
	if custom.NonceType == "FIXED" && len(values) == 0 {
		return fmt.Errorf("Mieru 固定 Nonce 至少需要一个十六进制字符串")
	}
	return nil
}

func mieruCustomProto(custom *MieruCustomPattern) *appctlpb.TrafficPattern {
	nonceType := mieruNonceTypes[custom.NonceType]
	entropyMode := mieruEntropyModes[custom.LowEntropyMode]
	rotation, _ := mieruRotation(custom.MaskRotation)
	pattern := &appctlpb.TrafficPattern{
		// Explicit false has the same behavior as an omitted UnlockAll. Its
		// presence distinguishes custom values that match a named preset.
		UnlockAll: proto.Bool(false),
		TcpFragment: &appctlpb.TCPFragment{
			Enable: proto.Bool(custom.TCPFragment), MaxSleepMs: proto.Int32(int32(custom.MaxSleepMs)),
		},
		Nonce: &appctlpb.NoncePattern{
			Type: nonceType.Enum(), ApplyToAllUDPPacket: proto.Bool(custom.NonceApplyToAllUDP),
			MinLen: proto.Int32(int32(custom.NonceMinLen)), MaxLen: proto.Int32(int32(custom.NonceMaxLen)),
		},
		Padding: &appctlpb.PaddingPattern{
			MaxMiddlePaddingLen: proto.Int32(int32(custom.PaddingMiddle)), MaxEndPaddingLen: proto.Int32(int32(custom.PaddingEnd)),
		},
		LowEntropy: &appctlpb.LowEntropyPattern{Mode: entropyMode.Enum(), MaskRotation: rotation.Enum()},
	}
	if custom.NonceType == "FIXED" {
		pattern.Nonce.CustomHexStrings, _ = mieruFixedHex(custom.NonceFixedHex)
	}
	return pattern
}

func mieruPattern(input Input) string {
	if input.MieruTrafficPattern == mieruTrafficCustom {
		return trafficpattern.Encode(mieruCustomProto(input.MieruCustomPattern))
	}
	return mieruTrafficPatterns[input.MieruTrafficPattern]
}

// Decode only values that the form can reproduce byte for byte. Unknown or
// partially specified patterns remain source-only to avoid losing options.
func parseMieruCustomPattern(encoded string) (*MieruCustomPattern, bool) {
	pattern, err := trafficpattern.Decode(encoded)
	if err != nil || trafficpattern.Validate(pattern) != nil || pattern.Seed != nil || pattern.UnlockAll == nil || pattern.GetUnlockAll() ||
		pattern.TcpFragment == nil || pattern.Nonce == nil || pattern.Padding == nil || pattern.LowEntropy == nil {
		return nil, false
	}
	fragment, nonce, padding, entropy := pattern.TcpFragment, pattern.Nonce, pattern.Padding, pattern.LowEntropy
	if fragment.Enable == nil || fragment.MaxSleepMs == nil || nonce.Type == nil || nonce.ApplyToAllUDPPacket == nil ||
		nonce.MinLen == nil || nonce.MaxLen == nil || padding.MaxMiddlePaddingLen == nil || padding.MaxEndPaddingLen == nil ||
		entropy.Mode == nil || entropy.MaskRotation == nil {
		return nil, false
	}
	custom := &MieruCustomPattern{
		TCPFragment: fragment.GetEnable(), MaxSleepMs: int(fragment.GetMaxSleepMs()),
		NonceApplyToAllUDP: nonce.GetApplyToAllUDPPacket(), NonceMinLen: int(nonce.GetMinLen()), NonceMaxLen: int(nonce.GetMaxLen()),
		NonceFixedHex: strings.Join(nonce.GetCustomHexStrings(), ", "),
		PaddingMiddle: int(padding.GetMaxMiddlePaddingLen()), PaddingEnd: int(padding.GetMaxEndPaddingLen()),
	}
	for name, value := range mieruNonceTypes {
		if value == nonce.GetType() {
			custom.NonceType = name
			break
		}
	}
	for bits, value := range mieruEntropyModes {
		if value == entropy.GetMode() {
			custom.LowEntropyMode = bits
			break
		}
	}
	custom.MaskRotation, _ = mieruRotationName(entropy.GetMaskRotation())
	if validateMieruCustomPattern(custom) != nil || trafficpattern.Encode(mieruCustomProto(custom)) != encoded {
		return nil, false
	}
	return custom, true
}
