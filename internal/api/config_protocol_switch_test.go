package api

import (
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestInboundProtocolSwitchKeepsAccountInstanceAndOtherInbounds(t *testing.T) {
	for _, engine := range core.AllEngines() {
		for _, shared := range []bool{false, true} {
			t.Run(string(engine)+map[bool]string{false: "/owner", true: "/shared"}[shared], func(t *testing.T) {
				db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
				agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "protocol-switch", engine)
				grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 32412, 32413)
				client, foreign, port, foreignPort := owner, recipient, 24441, 32412
				if shared {
					client, foreign, port, foreignPort = recipient, owner, 32412, 24441
				}
				base := "/agents/" + agent.ID + "/configs/" + string(engine)
				plan := func(protocol, tag string, port int) serverconfig.Input {
					t.Helper()
					var input serverconfig.Input
					client.call("POST", base+"/plans", map[string]string{"protocol": protocol}, http.StatusCreated, &input)
					input.Tag, input.Port = tag, port
					if input.TLSEnabled && input.TLSMode == "" {
						input.CertificatePath, input.PrivateKeyPath = "/tmp/qch-fixture.crt", "/tmp/qch-fixture.key"
					}
					return input
				}
				add := func(target configScopeAPIClient, input serverconfig.Input, version int) configMutationResult {
					t.Helper()
					var saved configMutationResult
					target.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": version, "intent": "deploy", "input": input, "preserve_ss_rust_globals": true}, http.StatusOK, &saved)
					completeConfigScopeAPITask(t, ctx, db, saved.Task)
					return saved
				}
				retained := add(foreign, plan("ss2022", "switch-me", foreignPort), 0)
				saved := add(client, plan("ss2022", "switch-me", port), 0)
				other := plan("ss2022", "keep-me", port+1)
				saved = add(client, other, saved.Config.Version)
				protocol := "shadowsocks"
				if engine == core.EngineMihomo || engine == core.EngineXray {
					protocol = "vmess"
				}
				next := plan(protocol, "switch-me", port)
				body := map[string]any{"operation": "modify", "original_tag": "switch-me", "expected_version": saved.Config.Version, "intent": "deploy", "input": next, "preserve_ss_rust_globals": true}
				body["expected_version"] = saved.Config.Version - 1
				client.call("POST", base+"/server-inbounds", body, http.StatusConflict, nil)
				body["expected_version"] = saved.Config.Version
				invalid := next
				invalid.Protocol = "unknown-protocol"
				body["input"] = invalid
				client.call("POST", base+"/server-inbounds", body, http.StatusBadRequest, nil)
				body["input"] = next
				client.call("POST", base+"/server-inbounds", body, http.StatusOK, &saved)
				if saved.Task.SharedInstance != shared || saved.Task.ConfigID != saved.Config.ID || saved.Task.ConfigVersion != saved.Config.Version {
					t.Fatal("protocol switch deployed another account or configuration")
				}
				completeConfigScopeAPITask(t, ctx, db, saved.Task)
				inbounds := serverconfig.ParseAll(engine, saved.Config.Content)
				if len(inbounds) != 2 || inbounds[0].Tag != next.Tag || inbounds[0].Protocol != next.Protocol || inbounds[0].Port != port || inbounds[0].Credential != next.Credential || inbounds[1].Tag != other.Tag || inbounds[1].Credential != other.Credential {
					t.Fatal("protocol switch lost target identity, parameters or another inbound")
				}
				var untouched core.Config
				foreign.call("GET", base, nil, http.StatusOK, &untouched)
				if untouched.ID != retained.Config.ID || untouched.Version != retained.Config.Version || untouched.Content != retained.Config.Content {
					t.Fatal("protocol switch changed another user's instance")
				}
			})
		}
	}
}
