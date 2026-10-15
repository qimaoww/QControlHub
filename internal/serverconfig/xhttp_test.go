package serverconfig

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func newXHTTPTestPlan(t *testing.T, key string) Input {
	t.Helper()
	protocol, ok := FindProtocol(core.EngineXray, key)
	if !ok {
		t.Fatal(key)
	}
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.Listen, input.ClientAddress = "::", "2001:db8::10"
	input.ClientHost, input.XHTTPMode = "", "auto"
	return input
}

func TestXHTTPStandaloneAndGroupRoundTrip(t *testing.T) {
	for _, key := range []string{ProtocolVLESSXHTTP, ProtocolXHTTPTLS, ProtocolXHTTPTLSReality, ProtocolXHTTPRealityTLS, ProtocolXHTTPFive} {
		t.Run(key, func(t *testing.T) {
			input := newXHTTPTestPlan(t, key)
			if input.RealityEnabled {
				input.RealityServerNames = "first.example.com," + input.RealityServerName
			}
			input.XHTTPServerExtra = `{"scMaxEachPostBytes":1000000,"scMaxBufferedPosts":30}`
			input.XHTTPExtra = `{"xmux":{"maxConcurrency":"8-16","maxConnections":0,"hMaxReusableSecs":"60-120"}}`
			content, err := Generate(core.EngineXray, input)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(content, "downloadSettings") || strings.Contains(content, "xmux") || strings.Contains(content, input.ClientAddress) {
				t.Fatal("client values leaked into server source")
			}
			metadata, err := MarshalClientMetadata(input)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(metadata, input.RealityPrivateKey) && input.RealityPrivateKey != "" {
				t.Fatal("server secret leaked into client metadata")
			}
			inputs := ParseAll(core.EngineXray, content)
			if len(inputs) != 1 {
				t.Fatalf("expected one editable owner, got %+v", inputs)
			}
			parsed := inputs[0]
			if err := ApplyClientMetadata(&parsed, metadata); err != nil {
				t.Fatal(err)
			}
			if input.RealityEnabled && parsed.RealityServerName != input.RealityServerName {
				t.Fatal("selected Reality SNI changed after reload")
			}
			if isXHTTPGroup(key) && parsed.TLSALPN != "h2" {
				t.Fatal("group TLS ALPN changed after reload")
			}
			if parsed.Protocol != key || parsed.ClientAddress != input.ClientAddress || parsed.XHTTPExtra != input.XHTTPExtra || parsed.XHTTPMode != input.XHTTPMode || parsed.TransportHost != input.TransportHost {
				t.Fatalf("round trip lost parameters: %+v", parsed)
			}
			regenerated, err := Generate(core.EngineXray, parsed)
			if err != nil {
				t.Fatal(err)
			}
			var a, b any
			_ = json.Unmarshal([]byte(content), &a)
			_ = json.Unmarshal([]byte(regenerated), &b)
			before, _ := json.Marshal(a)
			after, _ := json.Marshal(b)
			if string(before) != string(after) {
				t.Fatalf("native source changed after reload\nbefore %s\nafter %s", before, after)
			}
			profile, err := BuildClientProfile(parsed, "203.0.113.10", "")
			if err != nil {
				t.Fatal(err)
			}
			if profile.Xray == "" {
				t.Fatal("missing full client JSON")
			}
			want := 0
			if key == ProtocolXHTTPFive {
				want = 5
			} else if isXHTTPGroup(key) {
				want = 1
			}
			if len(profile.Variants) != want {
				t.Fatalf("expected %d variants, got %d", want, len(profile.Variants))
			}
			for _, secret := range []string{input.RealityPrivateKey, input.PrivateKeyPath, input.CertificatePath} {
				if secret != "" && strings.Contains(profile.Xray, secret) {
					t.Fatal("server material in export")
				}
			}
			frontends, err := ManagedTLSFrontends(core.EngineXray, content, map[string]string{input.Tag: metadata})
			if err != nil {
				t.Fatal(err)
			}
			if isXHTTPGroup(key) != (len(frontends) == 1) {
				t.Fatalf("unexpected dependency: %+v", frontends)
			}
			accounting, err := PlanPresetAccounting(core.EngineXray, content)
			if err != nil {
				t.Fatal(err)
			}
			if len(accounting.Ports) != 1 || accounting.Ports[0].Port != input.Port {
				t.Fatalf("internal listener became billable: %+v", accounting.Ports)
			}
			if _, err := PrepareAccounting(core.EngineXray, accounting.Content); err != nil {
				t.Fatal("accounting is not idempotent", err)
			}
			if _, err := SharedTrafficEndpoints(core.EngineXray, accounting.Content); err != nil {
				t.Fatal("shared ownership rejected", err)
			}
		})
	}
}

func TestXHTTPDownloadRoundTripAndIPv6URI(t *testing.T) {
	for _, key := range []string{ProtocolVLESSXHTTP, ProtocolXHTTPTLS} {
		input := newXHTTPTestPlan(t, key)
		if input.TLSEnabled {
			input.ClientAddress, input.ClientHost = "cdn-one.example.com", "tls.example.com"
		}
		d := xhttpEndpoint(input, input.TLSEnabled)
		d["address"], d["port"] = "203.0.113.9", input.Port
		d["sockopt"] = map[string]any{"tcpKeepAliveIdle": 20}
		mapValue(d["xhttpSettings"])["extra"] = map[string]any{"xmux": map[string]any{"maxConcurrency": 8, "maxConnections": 0}}
		extra, _ := json.Marshal(map[string]any{"downloadSettings": d, "noGRPCHeader": true})
		input.XHTTPExtra = string(extra)
		if _, err := Generate(core.EngineXray, input); err != nil {
			t.Fatal(err)
		}
		profile, err := BuildClientProfile(input, "203.0.113.1", "")
		if err != nil {
			t.Fatal(err)
		}
		if profile.SubscriptionCompatible || profile.URIError == "" || profile.MihomoError == "" {
			t.Fatal("complex export was silently downgraded")
		}
		encoded, _ := json.Marshal(profile)
		if !strings.Contains(string(encoded), `"mihomo_error"`) {
			t.Fatal("client UI cannot read the compatibility reason")
		}
		var root map[string]any
		_ = json.Unmarshal([]byte(profile.Xray), &root)
		x := mapValue(mapValue(firstMap(root["outbounds"])["streamSettings"])["xhttpSettings"])
		got, _ := json.Marshal(mapValue(x["extra"])["downloadSettings"])
		want, _ := json.Marshal(d)
		if string(got) != string(want) {
			t.Fatalf("downloadSettings lost fields: %s", got)
		}
		input.XHTTPExtra = ""
		input.ClientAddress = "2001:db8::10"
		profile, err = BuildClientProfile(input, "203.0.113.1", "")
		if err != nil {
			t.Fatal(err)
		}
		uri, _ := url.Parse(profile.URI)
		if uri.Hostname() != input.ClientAddress || uri.Query().Get("path") != input.TransportPath {
			t.Fatal("IPv6 URI encoding broken", profile.URI)
		}
	}
}

func TestXHTTPGroupLifecycleAndDetachedMetadata(t *testing.T) {
	input := newXHTTPTestPlan(t, ProtocolXHTTPFive)
	content, err := Generate(core.EngineXray, input)
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := MarshalClientMetadata(input)
	for _, operation := range []string{"modify", "delete"} {
		changed := input
		changed.Tag += "-new"
		changed.Port = 8443
		generated, err := Generate(core.EngineXray, changed)
		if err != nil {
			t.Fatal(err)
		}
		merged, err := MutateGenerated(core.EngineXray, content, generated, input.Tag, operation)
		if err != nil {
			t.Fatal(operation, err)
		}
		var root map[string]any
		_ = json.Unmarshal([]byte(merged), &root)
		inbounds, _ := json.Marshal(root["inbounds"])
		if strings.Contains(string(inbounds), `"`+input.Tag+`"`) || strings.Contains(string(inbounds), `"`+input.Tag+`.xh"`) {
			t.Fatal("old group members survived", merged)
		}
		if operation == "modify" && len(ParseAll(core.EngineXray, merged)) != 1 {
			t.Fatal("group replacement was not atomic")
		}
	}
	duplicate := input
	duplicate.Tag += "-second"
	duplicate.Port = 8443
	generated, _ := Generate(core.EngineXray, duplicate)
	if _, err := MutateGenerated(core.EngineXray, content, generated, "", "add"); err == nil {
		t.Fatal("internal listener conflict accepted")
	}
	detached := strings.Replace(content, `"xver": 0`, `"xver": 1`, 1)
	if _, err := ManagedTLSFrontends(core.EngineXray, detached, map[string]string{input.Tag: metadata}); err == nil {
		t.Fatal("detached graph was accepted")
	}
	parsed, _ := Parse(core.EngineXray, content)
	parsed.TransportPath = "/changed"
	_ = ApplyClientMetadata(&parsed, metadata)
	if parsed.ClientAddress != "" {
		t.Fatal("stale metadata hydrated edited source")
	}
}

func TestXHTTPValidationExplainsConflicts(t *testing.T) {
	for _, test := range []struct {
		name    string
		change  func(*Input)
		message string
	}{
		{"flow", func(p *Input) { p.Flow = "xtls-rprx-vision" }, "flow"},
		{"mode", func(p *Input) { p.XHTTPMode = "bad" }, "mode"},
		{"host", func(p *Input) { p.TransportHost = "server.example"; p.ClientHost = "other.example" }, "Host"},
		{"xmux", func(p *Input) { p.XHTTPExtra = `{"xmux":{"maxConcurrency":8,"maxConnections":2}}` }, "maxConnections"},
		{"old lifetime", func(p *Input) { p.XHTTPExtra = `{"xmux":{"cMaxLifetimeMs":1000}}` }, "hMaxReusableSecs"},
		{"server download", func(p *Input) { p.XHTTPServerExtra = `{"downloadSettings":{}}` }, "客户端专属"},
		{"private target", func(p *Input) { p.RealityTarget = "127.0.0.1:443" }, "公网"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := newXHTTPTestPlan(t, ProtocolVLESSXHTTP)
			test.change(&input)
			_, err := Generate(core.EngineXray, input)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("want %s, got %v", test.message, err)
			}
		})
	}
}

func TestXHTTPHistoricalAliasesAndTLSIdentification(t *testing.T) {
	input := newXHTTPTestPlan(t, ProtocolXHTTPTLS)
	content, _ := Generate(core.EngineXray, input)
	content = strings.ReplaceAll(strings.ReplaceAll(content, `"xhttp"`, `"splithttp"`), `"xhttpSettings"`, `"splithttpSettings"`)
	parsed, ok := Parse(core.EngineXray, content)
	if !ok || parsed.Protocol != ProtocolXHTTPTLS || !parsed.TLSEnabled || parsed.RealityEnabled {
		t.Fatalf("TLS was misidentified: %+v", parsed)
	}
}

func TestXHTTPRegenerateKeepsDownloadAssociation(t *testing.T) {
	for _, key := range []string{ProtocolVLESSXHTTP, ProtocolXHTTPTLS} {
		input := newXHTTPTestPlan(t, key)
		input.ClientPort = input.Port
		download := xhttpEndpoint(input, input.TLSEnabled)
		download["address"], download["port"] = "203.0.113.8", input.Port
		download["sockopt"] = map[string]any{"tcpKeepAliveIdle": 20}
		value, _ := json.Marshal(map[string]any{"downloadSettings": download})
		input.XHTTPExtra = string(value)
		protocol, _ := FindProtocol(core.EngineXray, key)
		plan, err := RegeneratePlan(protocol, input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Generate(core.EngineXray, plan); err != nil {
			t.Fatal("regenerated split plan is invalid", err)
		}
		extra, _ := xhttpObject(plan.XHTTPExtra)
		got := mapValue(extra["downloadSettings"])
		if got["address"] != download["address"] || got["sockopt"] == nil || intValue(got["port"]) != plan.Port || mapValue(got["xhttpSettings"])["path"] != plan.TransportPath {
			t.Fatal("download references or advanced options lost", got)
		}
		if input.RealityEnabled && mapValue(got["realitySettings"])["publicKey"] != plan.RealityPublicKey {
			t.Fatal("download public key still uses previous identity")
		}
	}
}

func TestXHTTPGroupAccountingRejectsSharedUser(t *testing.T) {
	input := newXHTTPTestPlan(t, ProtocolXHTTPFive)
	content, _ := Generate(core.EngineXray, input)
	second := input
	second.Tag += "-second"
	second.Port, second.XHTTPTLSPort, second.XHTTPBackendPort = 8443, 59001, 59002
	generated, _ := Generate(core.EngineXray, second)
	merged, err := MutateGenerated(core.EngineXray, content, generated, "", "add")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanPresetAccounting(core.EngineXray, merged); err == nil || !strings.Contains(err.Error(), "用户名") {
		t.Fatal("shared user statistics would mix instance quotas", err)
	}
}

func TestXHTTPHTTP1ExportKeepsTLSVerification(t *testing.T) {
	input := newXHTTPTestPlan(t, ProtocolXHTTPTLS)
	input.TLSClientALPN, input.XHTTPMode = "http/1.1", "stream-one"
	profile, err := BuildClientProfile(input, "203.0.113.1", "")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal([]byte(profile.Xray), &root)
	tls := mapValue(mapValue(firstMap(root["outbounds"])["streamSettings"])["tlsSettings"])
	if tls["fingerprint"] != "unsafe" || tls["allowInsecure"] != false || profile.Mihomo != "" || !strings.Contains(profile.MihomoError, "HTTP/1.1") {
		t.Fatal("HTTP/1.1 export silently changed TLS or compatibility", profile)
	}
}
