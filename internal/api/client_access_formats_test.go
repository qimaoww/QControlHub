package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"gopkg.in/yaml.v3"
)

func TestClientAccessMihomoFormatsPreserveClientValues(t *testing.T) {
	protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolSS2022)
	input, err := serverconfig.NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := serverconfig.BuildClientProfileNamed(input, "2001:db8::10", "", "Tokyo: [edge] #1")
	if err != nil {
		t.Fatal(err)
	}
	entries := []clientAccessEntry{{Profiles: []clientAccessProfile{{Profile: profile}}, AddressOptions: []clientAccessAddressOption{{Profiles: []clientAccessProfile{{Profile: profile}}}}}}
	prepareClientAccessFormats(entries)
	for _, item := range []clientAccessProfile{entries[0].Profiles[0], entries[0].AddressOptions[0].Profiles[0]} {
		if item.MihomoError != "" || !strings.HasPrefix(item.MihomoYAML, "{") || strings.ContainsAny(item.MihomoYAML, "\r\n") || item.MihomoYAML != profile.Mihomo || item.Profile.URI != profile.URI {
			t.Fatal("missing YAML export or changed URL")
		}
		var document map[string]any
		if err := yaml.Unmarshal([]byte(item.MihomoYAML), &document); err != nil {
			t.Fatal(err)
		}
		var original map[string]any
		if err := yaml.Unmarshal([]byte(profile.Mihomo), &original); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(document, original) {
			t.Fatal("formatting changed names, IPv6 address, port or credentials")
		}
		if _, _, ok := subStoreMihomoNode(item.MihomoYAML); !ok {
			t.Fatal("display export must be accepted by the Sub-Store node parser")
		}
		renamed, err := renameSubStoreNode(item.MihomoYAML, "Renamed edge")
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{item.MihomoYAML, renamed} {
			var ordered yaml.Node
			if err := yaml.Unmarshal([]byte(value), &ordered); err != nil {
				t.Fatal(err)
			}
			for index, key := range []string{"name", "type", "server", "port", "cipher", "password", "udp"} {
				if ordered.Content[0].Content[index*2].Value != key {
					t.Fatalf("display and Sub-Store must preserve field %d as %s", index, key)
				}
			}
		}
	}
	encoded, err := json.Marshal(entries)
	if err != nil || !strings.Contains(string(encoded), `"mihomo_yaml":`) || strings.Contains(string(encoded), `"Mihomo":`) {
		t.Fatal("client endpoint serialization omitted its display format")
	}
}

func TestClientAccessMihomoFormatErrorsDoNotFallBackToURL(t *testing.T) {
	entries := []clientAccessEntry{{Profiles: []clientAccessProfile{
		{Profile: serverconfig.ClientProfile{URI: "existing-url", MihomoError: "unsupported protocol"}},
		{Profile: serverconfig.ClientProfile{URI: "existing-url", Mihomo: "invalid: ["}},
		{Profile: serverconfig.ClientProfile{URI: "existing-url"}},
	}}}
	prepareClientAccessFormats(entries)
	for _, profile := range entries[0].Profiles {
		if profile.MihomoYAML != "" || profile.MihomoError == "" || profile.Profile.URI != "existing-url" {
			t.Fatal("unsupported YAML must retain the original URL separately")
		}
	}
}
