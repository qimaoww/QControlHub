package core

import (
	"errors"
	"fmt"
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

// CoreSource identifies the publication source for a managed core. It is only
// meaningful for Mihomo development builds; an omitted source means the default
// official repository and is backward compatible.
type CoreSource string

const (
	CoreSourceOfficial CoreSource = "official"
	CoreSourceMirror   CoreSource = "mirror"
)

func (source CoreSource) Valid() bool {
	return source == CoreSourceOfficial || source == CoreSourceMirror
}

// NormalizeCoreSource validates a release source choice. An empty value is the
// legacy documented default (official MetaCubeX). A non-empty value is only
// accepted for Mihomo development builds; any other combination is rejected so
// the Agent can fail closed instead of silently switching repositories.
func NormalizeCoreSource(engine Engine, selector, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if engine != EngineMihomo || selector != CoreVersionDevelopment {
		return "", errors.New("内核来源仅适用于 Mihomo 开发版安装")
	}
	if !CoreSource(raw).Valid() {
		return "", fmt.Errorf("不支持的内核来源 %q", raw)
	}
	return string(CoreSource(raw)), nil
}
