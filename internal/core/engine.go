package core

import (
	"fmt"
	"slices"
	"strings"
)

type Engine string

const (
	EngineMihomo          Engine = "mihomo"
	EngineXray            Engine = "xray"
	EngineSingBox         Engine = "sing-box"
	EngineShadowsocksRust Engine = "ss-rust"
)

func (e Engine) Valid() bool {
	return slices.Contains(AllEngines(), e)
}

func ParseEngine(value string) (Engine, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "singbox" {
		value = string(EngineSingBox)
	}
	if value == "ssrust" || value == "shadowsocks-rust" || value == "shadowsocksrust" {
		value = string(EngineShadowsocksRust)
	}
	engine := Engine(value)
	if !engine.Valid() {
		return "", fmt.Errorf("unsupported engine %q", value)
	}
	return engine, nil
}
