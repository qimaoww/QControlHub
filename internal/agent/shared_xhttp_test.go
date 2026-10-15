//go:build linux

package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestSharedXHTTPAgentAuthorization(t *testing.T) {
	for _, key := range []string{serverconfig.ProtocolVLESSXHTTP, serverconfig.ProtocolXHTTPTLS, serverconfig.ProtocolXHTTPTLSReality, serverconfig.ProtocolXHTTPRealityTLS, serverconfig.ProtocolXHTTPFive} {
		t.Run(key, func(t *testing.T) {
			protocol, _ := serverconfig.FindProtocol(core.EngineXray, key)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			manager, _, policies, _ := sharedTrafficFixture(t)
			// The third fixture allocation owns just one Xray ingress. Internal
			// group ports must remain outside the billed public endpoint list.
			policy := policies[2]
			input.Port, input.ClientPort = policy.Port, policy.Port
			input.BlockMainlandDestination, input.BlockMainlandSource = false, false
			content, err := serverconfig.Generate(core.EngineXray, input)
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := serverconfig.MarshalClientMetadata(input)
			if err != nil {
				t.Fatal(err)
			}
			frontends, err := serverconfig.ManagedTLSFrontends(core.EngineXray, content, map[string]string{input.Tag: metadata})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if err := manager.SetPolicies(ctx, policies, "agt_shared"); err != nil {
				t.Fatal(err)
			}
			executions := 0
			client := &Client{config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "agent.json")}, traffic: manager,
				executeFunc: func(_ context.Context, task core.Task) (string, error) {
					executions++
					if !task.SharedInstance || task.ConfigContent != content || len(task.TLSFrontends) != len(frontends) {
						t.Fatal("shared authorization changed the private deployment")
					}
					return "deployed", nil
				}}
			task := core.Task{ID: "tsk_0000000000000001", Action: core.ActionDeploy, Engine: core.EngineXray, SharedTrafficID: policy.SharedQuota.ID, SharedInstance: true, ConfigContent: content, TLSFrontends: frontends}
			if result := client.resultForTask(ctx, task); !result.Success || executions != 1 {
				t.Fatalf("recipient cannot deploy through the Agent's quota gate: %+v", result)
			}
			policy.SharedQuota.Revoked = true
			policies[2] = policy
			if err := manager.SetPolicies(ctx, policies, "agt_shared"); err != nil {
				t.Fatal(err)
			}
			task.ID = "tsk_0000000000000002"
			if result := client.resultForTask(ctx, task); result.Success || executions != 1 {
				t.Fatal("revoked recipient reached core execution")
			}
		})
	}
}
