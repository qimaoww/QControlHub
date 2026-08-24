package netpolicy

import (
	"net/netip"
	"testing"
)

func TestIsPublicAddressRejectsIANAUnsupportedRanges(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"93.184.216.34", "2606:4700:4700::1111", "::ffff:93.184.216.34",
		"198.20.0.1", "2620:4f:8001::1",
	} {
		if !IsPublicAddress(netip.MustParseAddr(value)) {
			t.Errorf("globally routable address %s was rejected", value)
		}
	}

	for _, value := range []string{
		"0.0.0.0", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.0.1",
		"192.0.2.1", "192.31.196.1", "192.52.193.1", "192.175.48.1",
		"198.18.0.1", "198.19.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1",
		"::", "::1", "64:ff9b::1", "100::1", "100:0:0:1::1", "2001:db8::1",
		"2002::1", "2620:4f:8000::1", "3fff::1", "5f00::1", "fc00::1", "fe80::1",
		"2606:4700:4700::1111%eth0",
	} {
		if IsPublicAddress(netip.MustParseAddr(value)) {
			t.Errorf("IANA special-purpose address %s was accepted", value)
		}
	}
}
