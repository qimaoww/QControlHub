package api

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// Expose the same single-line proxy map consumed by Sub-Store, using only
// already authorized, deployed client data.
func prepareClientAccessFormats(entries []clientAccessEntry) {
	prepare := func(profiles []clientAccessProfile) {
		for index := range profiles {
			item := &profiles[index]
			item.MihomoYAML = ""
			item.MihomoError = item.Profile.MihomoError
			if item.MihomoError != "" {
				continue
			}
			value := item.Profile.Mihomo
			var proxy map[string]any
			if err := yaml.Unmarshal([]byte(value), &proxy); err != nil || len(proxy) == 0 || !strings.HasPrefix(value, "{") || strings.ContainsAny(value, "\r\n") {
				item.MihomoError = "此配置暂不支持 Mihomo YAML"
				continue
			}
			item.MihomoYAML = value
		}
	}
	for index := range entries {
		prepare(entries[index].Profiles)
		for option := range entries[index].AddressOptions {
			prepare(entries[index].AddressOptions[option].Profiles)
		}
	}
}
