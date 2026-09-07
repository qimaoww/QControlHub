package core

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeTCPSettings(t *testing.T) {
	input := TCPSettings{"net.ipv4.tcp_congestion_control": " bbr ", "net.ipv4.tcp_rmem": "4096\t87380  16777216", "net.ipv4.tcp_ecn": "02"}
	got, err := NormalizeTCPSettings(input)
	want := TCPSettings{"net.ipv4.tcp_congestion_control": "bbr", "net.ipv4.tcp_rmem": "4096 87380 16777216", "net.ipv4.tcp_ecn": "2"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, %v", got, err)
	}
	if input["net.ipv4.tcp_ecn"] != "02" {
		t.Fatal("mutated caller input")
	}
	if got, err := NormalizeTCPSettings(TCPSettings{"net.core.default_qdisc": "fq_pie"}); err != nil || got["net.core.default_qdisc"] != "fq_pie" {
		t.Fatalf("FQ-PIE rejected: %v %v", got, err)
	}
	for name, settings := range map[string]TCPSettings{
		"empty": {}, "unknown": {"kernel.core_pattern": "|/bin/sh"},
		"newline":     {"net.core.default_qdisc": "fq\nnet.ipv4.ip_forward=1"},
		"shell":       {"net.ipv4.tcp_congestion_control": "bbr; reboot"},
		"module path": {"net.ipv4.tcp_congestion_control": "../../bbr"},
		"range":       {"net.ipv4.tcp_ecn": "9"}, "negative": {"net.core.wmem_max": "-1"},
		"overflow":     {"net.core.wmem_max": "99999999999999999999"},
		"signed":       {"net.ipv4.tcp_ecn": "+1"},
		"overlong":     {"net.ipv4.tcp_ecn": strings.Repeat("0", 101)},
		"tuple length": {"net.ipv4.tcp_rmem": "4096 8192"},
		"tuple order":  {"net.ipv4.tcp_rmem": "8192 4096 16384"},
		"tuple size":   {"net.ipv4.tcp_rmem": "4096 8192 2147483648"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeTCPSettings(settings); err == nil {
				t.Fatal("accepted invalid settings")
			}
		})
	}
	for _, action := range []Action{ActionEnableBBR, ActionDisableBBR, ActionConfigureTCP} {
		if !action.Valid() || !action.SystemBBR() || !action.AgentLevel() {
			t.Fatalf("invalid TCP action %s", action)
		}
	}
}
