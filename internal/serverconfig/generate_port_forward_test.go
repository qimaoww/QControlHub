package serverconfig

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPortForwardPlansGenerateNativeSyntaxAndRoundTrip(t *testing.T) {
	t.Parallel()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		engine := engine
		t.Run(string(engine), func(t *testing.T) {
			t.Parallel()
			input := Input{
				Protocol: ProtocolPortForward, Tag: "forward-in", Listen: "0.0.0.0", Port: 28080,
				Transport: "raw", TargetAddress: "10.20.30.40", TargetPort: 8080, Network: "tcp,udp",
			}
			content, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			if err := core.ValidateConfig(engine, content); err != nil {
				t.Fatalf("generated configuration is invalid: %v\n%s", err, content)
			}
			parsed, ok := Parse(engine, content)
			if !ok || parsed.Protocol != ProtocolPortForward || parsed.Tag != input.Tag || parsed.Listen != input.Listen ||
				parsed.Port != input.Port || parsed.TargetAddress != input.TargetAddress || parsed.TargetPort != input.TargetPort || parsed.Network != input.Network {
				t.Fatalf("Parse(%s) = %+v, %v\n%s", engine, parsed, ok, content)
			}
			switch engine {
			case core.EngineMihomo:
				for _, want := range []string{"type: tunnel", "target: 10.20.30.40:8080", "- tcp", "- udp"} {
					if !strings.Contains(content, want) {
						t.Errorf("Mihomo port forward is missing %q:\n%s", want, content)
					}
				}
			case core.EngineXray:
				for _, want := range []string{`"protocol": "tunnel"`, `"allowedNetwork": "tcp,udp"`, `"rewriteAddress": "10.20.30.40"`, `"rewritePort": 8080`} {
					if !strings.Contains(content, want) {
						t.Errorf("Xray port forward is missing %q:\n%s", want, content)
					}
				}
			case core.EngineSingBox:
				for _, want := range []string{`"type": "direct"`, `"override_address": "10.20.30.40"`, `"override_port": 8080`} {
					if !strings.Contains(content, want) {
						t.Errorf("sing-box port forward is missing %q:\n%s", want, content)
					}
				}
				if strings.Contains(content, `"network"`) {
					t.Errorf("sing-box must omit network to enable both TCP and UDP:\n%s", content)
				}
			}
		})
	}
}

func TestPortForwardPlanDefaultsRegenerateAndValidate(t *testing.T) {
	t.Parallel()
	protocol, ok := FindProtocol(core.EngineMihomo, ProtocolPortForward)
	if !ok || !protocol.PortForward {
		t.Fatal("Mihomo port-forward preset not found")
	}
	plan, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetAddress != "127.0.0.1" || plan.TargetPort != 80 || plan.Network != "tcp" || plan.Credential != "" {
		t.Fatalf("port-forward defaults = %+v", plan)
	}
	current := plan
	current.TargetAddress = "backend.example.com"
	current.TargetPort = 9443
	current.Network = "udp"
	regenerated, err := RegeneratePlan(protocol, current)
	if err != nil {
		t.Fatal(err)
	}
	if regenerated.Tag == current.Tag || regenerated.Port == current.Port {
		t.Fatalf("regeneration did not replace generated values: current=%+v regenerated=%+v", current, regenerated)
	}
	if regenerated.TargetAddress != current.TargetAddress || regenerated.TargetPort != current.TargetPort || regenerated.Network != current.Network {
		t.Fatalf("regeneration discarded the selected target: current=%+v regenerated=%+v", current, regenerated)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Input)
		want   string
	}{
		{name: "target", mutate: func(input *Input) { input.TargetAddress = "bad_target" }, want: "有效域名"},
		{name: "target port", mutate: func(input *Input) { input.TargetPort = 0 }, want: "目标端口"},
		{name: "network", mutate: func(input *Input) { input.Network = "icmp" }, want: "转发协议"},
		{name: "transport", mutate: func(input *Input) { input.Transport = "websocket" }, want: "原生 TCP/UDP"},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := current
			test.mutate(&invalid)
			if _, err := Generate(core.EngineMihomo, invalid); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Generate() error = %v, want %q", err, test.want)
			}
		})
	}
}
