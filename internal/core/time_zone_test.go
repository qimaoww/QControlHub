package core

import "testing"

func TestValidTimeZoneUsesIANAZoneDatabase(t *testing.T) {
	for _, zone := range []string{"UTC", "Asia/Shanghai", "Asia/Hong_Kong", "America/New_York", "Pacific/Apia"} {
		if !ValidTimeZone(zone) {
			t.Errorf("%q should be a valid IANA time zone", zone)
		}
	}
	for _, zone := range []string{"", "Local", "Invalid/Zone", "/etc/localtime", "../UTC", "America\\New_York", " Asia/Tokyo"} {
		if ValidTimeZone(zone) {
			t.Errorf("%q should not be accepted as an IANA time zone", zone)
		}
	}
}
