package api

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// Project only the already authorized, deployed client data. The internal
// flow-style export stays unchanged for Sub-Store and other consumers.
func prepareClientAccessFormats(entries []clientAccessEntry) {
	prepare := func(profiles []clientAccessProfile) {
		for index := range profiles {
			item := &profiles[index]
			item.MihomoYAML = ""
			item.MihomoError = item.Profile.MihomoError
			if item.MihomoError != "" {
				continue
			}
			var proxy map[string]any
			if err := yaml.Unmarshal([]byte(item.Profile.Mihomo), &proxy); err != nil || len(proxy) == 0 {
				item.MihomoError = "此配置暂不支持 Mihomo YAML"
				continue
			}
			var output strings.Builder
			encoder := yaml.NewEncoder(&output)
			encoder.SetIndent(2)
			err := encoder.Encode(map[string]any{"proxies": []map[string]any{proxy}})
			_ = encoder.Close()
			if err != nil {
				item.MihomoError = "Mihomo YAML 生成失败"
				continue
			}
			item.MihomoYAML = output.String()
		}
	}
	for index := range entries {
		prepare(entries[index].Profiles)
		for option := range entries[index].AddressOptions {
			prepare(entries[index].AddressOptions[option].Profiles)
		}
	}
}
