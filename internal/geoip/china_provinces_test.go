package geoip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestChinaProvinceNames(t *testing.T) {
	for input, want := range map[string]string{
		"Guangdong": "广东", "Guangdong Sheng": "广东", "广东省": "广东",
		"Inner Mongolia": "内蒙古", "内蒙古自治区": "内蒙古", "Ningxia Hui Autonomous Region": "宁夏",
		"Xinjiang Uygur Autonomous Region": "新疆", "广西壮族自治区": "广西",
		"Beijing": "北京", "Shanghai Municipality": "上海", "Shaanxi": "陕西", "Shanxi": "山西",
		"Xizang": "西藏", "Unknown": "", "Shenzhen": "", "": "",
	} {
		if got := chinaProvince(input); got != want {
			t.Errorf("%q = %q, want %q", input, got, want)
		}
	}
}

func TestLookupIncludesProvinceOnlyForChina(t *testing.T) {
	for _, test := range []struct{ body, code, province string }{
		{`{"country_code":"CN","country":"China","region":"Guangdong"}`, "CN", "广东"},
		{`{"country_code":"US","country":"United States","region":"California"}`, "US", ""},
		{`{"country_code":"CN","country":"China","region":"Unknown"}`, "CN", ""},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(test.body)) }))
		client := New(server.Client())
		client.endpoint = server.URL
		client.ipWhoEndpoint = ""
		client.freeIPAPIEndpoint = ""
		region, err := client.Lookup(context.Background(), netip.MustParseAddr("8.8.8.8"))
		server.Close()
		if err != nil || region.ISOCode != test.code || region.Province != test.province {
			t.Fatalf("region=%+v err=%v", region, err)
		}
	}
}
