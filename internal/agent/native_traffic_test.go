//go:build linux

package agent

import (
	"math"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"google.golang.org/protobuf/encoding/protowire"
)

func nativeStatWire(name string, count uint64) []byte {
	entry := protowire.AppendTag(nil, 1, protowire.BytesType)
	entry = protowire.AppendString(entry, name)
	entry = protowire.AppendTag(entry, 2, protowire.VarintType)
	entry = protowire.AppendVarint(entry, count)
	return protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), entry)
}
func TestNativeTrafficWireAndLegDirections(t *testing.T) {
	var wire []byte
	for name, value := range map[string]uint64{
		"inbound>>>a>>>traffic>>>uplink":           10,
		"inbound>>>a>>>traffic>>>downlink":         1000,
		"outbound>>>a-direct>>>traffic>>>uplink":   9,
		"outbound>>>a-direct>>>traffic>>>downlink": 990,
		"outbound>>>b-direct>>>traffic>>>downlink": 99999,
	} {
		wire = append(wire, nativeStatWire(name, value)...)
	}
	var result nativeStatsResponse
	if err := (nativeStatsCodec{}).Unmarshal(wire, &result); err != nil {
		t.Fatal(err)
	}
	legs := nativeTrafficLegs(result.Counters, serverconfig.AccountingPort{Inbound: "a", Outbounds: []string{"a-direct"}})
	if legs != (trafficLegs{ClientReceived: 10, ClientSent: 1000, TargetReceived: 990, TargetSent: 9}) {
		t.Fatalf("wrong legs: %+v", legs)
	}
	request, err := (nativeStatsCodec{}).Marshal(&nativeStatsRequest{})
	if err != nil || len(request) != 0 {
		t.Fatal("stats request must never set reset")
	}
}
func TestNativeTrafficRejectsMalformedCounters(t *testing.T) {
	for _, wire := range [][]byte{{0xff}, {0x08, 1}, nativeStatWire("", 1), nativeStatWire("negative", math.MaxUint64), append(nativeStatWire("duplicate", 1), nativeStatWire("duplicate", 2)...)} {
		var result nativeStatsResponse
		if err := (nativeStatsCodec{}).Unmarshal(wire, &result); err == nil {
			t.Fatalf("accepted %x", wire)
		}
	}
}
