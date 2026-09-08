package geoip

import (
	"slices"
	"testing"
)

func TestRegionCatalog(t *testing.T) {
	codes := RegionCodes()
	if len(codes) != 249 || !slices.IsSorted(codes) || len(slices.Compact(slices.Clone(codes))) != len(codes) {
		t.Fatalf("unexpected country/region catalog (%d): %v", len(codes), codes)
	}
	for _, code := range []string{"CN", "HK", "MO", "TW", "US", "SG", "JP", "AQ", "AX", "BQ", "SS", "UM", " us "} {
		if !ValidRegionCode(code) {
			t.Errorf("valid region rejected: %q", code)
		}
	}
	for _, code := range []string{"", "ZZ", "AA", "EU", "UN", "SU", "001", "USA", "../cn", "<script>"} {
		if ValidRegionCode(code) {
			t.Errorf("invalid region accepted: %q", code)
		}
	}
	codes[0] = "ZZ"
	if ValidRegionCode("ZZ") || !ValidRegionCode("AD") {
		t.Fatal("caller mutated the validation catalog")
	}
}
