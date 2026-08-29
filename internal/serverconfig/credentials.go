package serverconfig

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

func NewCredential(protocol, method string) (string, error) {
	if protocol == ProtocolSS2022 {
		size := 32
		if method == "2022-blake3-aes-128-gcm" {
			size = 16
		}
		value := make([]byte, size)
		if _, err := rand.Read(value); err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(value), nil
	}
	if protocol != ProtocolVLESS && protocol != ProtocolVLESSXHTTP && !isVLESSEncryptionProtocol(protocol) && protocol != ProtocolVMess && protocol != ProtocolSudoku {
		value := make([]byte, 24)
		if _, err := rand.Read(value); err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(value), nil
	}
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
