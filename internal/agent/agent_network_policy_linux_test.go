//go:build linux

package agent

import (
	"bytes"
	"io/fs"
	"testing"

	"github.com/qimaoww/qcontrolhub"
)

func TestAgentNetworkPolicyRecognizesOnlyShippedLegacy(t *testing.T) {
	for _, kind := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
		t.Run(kind, func(t *testing.T) {
			asset := "deploy/systemd/qagent.service"
			if kind == ServiceManagerOpenRC {
				asset = "deploy/openrc/qagent"
			}
			current, err := fs.ReadFile(qcontrolhub.AgentServiceAssets(), asset)
			if err != nil {
				t.Fatal(err)
			}
			legacy := bytes.ReplaceAll(current, []byte(" CAP_NET_RAW"), nil)
			if kind == ServiceManagerOpenRC {
				legacy = bytes.ReplaceAll(current, []byte("if qagent_capability_is_bound 13; then\n\tcapabilities=\"${capabilities},^cap_net_raw\"\nfi\n\n"), nil)
			}
			updated, change := agentNetworkPolicyUpgrade(legacy, current, kind)
			if !change || !bytes.Equal(updated, current) {
				t.Fatal("legacy binary-only upgrade not repaired")
			}
			for _, custom := range [][]byte{current, append(append([]byte{}, legacy...), []byte("# admin setting\n")...), bytes.ReplaceAll(legacy, []byte("root"), []byte("nobody"))} {
				if _, change := agentNetworkPolicyUpgrade(custom, current, kind); change {
					t.Fatal("current/custom policy was modified")
				}
			}
		})
	}
}

func TestAgentNetworkPolicyUpperBoundary(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"CapBnd:\t0000000000002000\n", true},
		{"CapBnd:\t0000000000000400\n", false},
		{"CapBnd: invalid\n", false},
		{"CapEff: 0000000000002000\n", false},
	} {
		if got := agentRawCapabilityBound(tc.value); got != tc.want {
			t.Fatalf("%q: %v", tc.value, got)
		}
	}
}

func TestAgentOpenRCPolicyPreservesOnlyEnvironmentOverrides(t *testing.T) {
	for _, line := range []string{"export QCH_AGENT_NAME='fixture'", `export QCH_AGENT_NAME='node'\''s name'`, "export QCH_ALLOW_HTTP='true'"} {
		if !agentOpenRCEnvironmentExport.MatchString(line) {
			t.Fatalf("installer export rejected: %s", line)
		}
	}
	for _, line := range []string{"capabilities=''", "command_user='nobody'", `export QCH_AGENT_NAME='x'; capabilities=''`, "export QCH_AGENT_NAME=$(id)", "export QCH_AGENT_NAME='x' # custom"} {
		if agentOpenRCEnvironmentExport.MatchString(line) {
			t.Fatalf("administrator shell policy accepted: %s", line)
		}
	}
}
