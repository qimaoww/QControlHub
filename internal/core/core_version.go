package core

import (
	"errors"
	"regexp"
	"strings"
)

const (
	CoreVersionStable      = "stable"
	CoreVersionDevelopment = "development"
)

var exactCoreVersionPattern = regexp.MustCompile(`^[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}(?:-[0-9A-Za-z][0-9A-Za-z.-]{0,31})?$`)

// NormalizeCoreVersionSelector accepts one of the two managed release
// channels or an exact upstream version. URLs, paths and command fragments are
// intentionally not part of this wire format.
func NormalizeCoreVersionSelector(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case CoreVersionStable:
		return CoreVersionStable, nil
	case CoreVersionDevelopment, "dev", "prerelease":
		return CoreVersionDevelopment, nil
	}
	value = strings.TrimPrefix(value, "v")
	if len(value) > 64 || !exactCoreVersionPattern.MatchString(value) {
		return "", errors.New("内核版本必须选择稳定版、开发版，或填写类似 1.19.0 / 1.14.0-beta.1 的完整版本号")
	}
	return value, nil
}
