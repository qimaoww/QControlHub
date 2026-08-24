package netpolicy

import (
	"net/netip"
	"testing"
)

func TestIsCloudflareAddressRecognizesPublishedEdgeRanges(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"104.22.17.83", "172.69.135.152", "162.158.193.59", "172.64.217.32",
		"172.71.124.82", "172.68.225.178", "2606:4700::1111", "2400:cb00::1",
		"::ffff:104.22.17.83",
	} {
		if !IsCloudflareAddress(netip.MustParseAddr(value)) {
			t.Errorf("Cloudflare edge address %s was not recognized", value)
		}
	}
	for _, value := range []string{"93.184.216.34", "1.1.1.1", "2001:4860:4860::8888"} {
		if IsCloudflareAddress(netip.MustParseAddr(value)) {
			t.Errorf("non-Cloudflare address %s was recognized as an edge", value)
		}
	}
}
