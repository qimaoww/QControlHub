package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestXHTTPTLSModesOwnershipAndSnapshotsAPI(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineMihomo} {
		for _, shared := range []bool{false, true} {
			for _, panelCertificate := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/shared=%t/panel=%t", engine, shared, panelCertificate), func(t *testing.T) {
					db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
					agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "xhttp-tls-modes", engine)
					agent.Features = append(agent.Features, core.AgentFeatureManagedTLS, core.AgentFeatureManagedTLSCertificates, core.AgentFeatureCertificates, core.AgentFeatureXHTTP, core.AgentFeatureXHTTPTLS, core.AgentFeatureNginxACME, core.AgentFeatureSharedCoreInstances)
					if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true}}}); err != nil {
						t.Fatal(err)
					}
					actor := owner
					if shared {
						grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 24443, 35001)
						actor = recipient
					}
					base := "/agents/" + agent.ID + "/configs/" + string(engine)
					var plan serverconfig.Input
					actor.call("POST", base+"/plans", map[string]string{"protocol": serverconfig.ProtocolXHTTPTLS}, http.StatusCreated, &plan)
					if plan.TLSMode != "caddy" || plan.TLSServerName != "" || plan.CertificatePath != "" || plan.Transport != "xhttp" {
						t.Fatal("dishonest or wrong defaults")
					}
					if shared && (plan.TLSPublicPort != 24443 || plan.Port != 35001) {
						t.Fatal("shared defaults ignored allocation", plan.Port, plan.TLSPublicPort)
					}
					plan.TLSServerName = "proxy.example.test"
					var certificate core.Certificate
					if panelCertificate {
						owner.call("POST", "/certificates", map[string]any{"name": "Proxy TLS", "source": "self-signed", "domains": []string{plan.TLSServerName}, "key_type": "ecdsa256", "days": 90}, 201, &certificate)
						if shared {
							owner.call("POST", "/certificates/"+certificate.ID+"/grants", map[string]string{"username": "bob"}, 204, nil)
						}
						plan.PanelCertificateID = certificate.ID
					}
					var last configMutationResult
					version := 0
					for index, mode := range []string{"caddy", "nginx", "native", "caddy"} {
						plan.TLSMode = mode
						plan.Listen = "127.0.0.1"
						plan.CertificatePath, plan.PrivateKeyPath = "", ""
						plan.TLSStaticHTML = "<!doctype html><p>owned page</p>"
						if mode == "native" {
							plan.Listen = "0.0.0.0"
							plan.CertificatePath, plan.PrivateKeyPath = "/etc/qcontrolhub/tls/test.crt", "/etc/qcontrolhub/tls/test.key"
							plan.TLSStaticHTML = ""
						}
						operation, original := "add", ""
						if index > 0 {
							operation, original = "modify", plan.Tag
						}
						body := map[string]any{"operation": operation, "original_tag": original, "expected_version": version, "intent": "deploy", "input": plan}
						if panelCertificate && index == 0 || index == 1 || index == 2 {
							var old []string
							for _, feature := range agent.Features {
								if feature != core.AgentFeatureManagedTLSCertificates {
									old = append(old, feature)
								}
							}
							if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: old, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true}}}); err != nil {
								t.Fatal(err)
							}
							actor.call("POST", base+"/server-inbounds", body, 409, nil)
							if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true}}}); err != nil {
								t.Fatal(err)
							}
						}
						if index == 0 || engine == core.EngineMihomo && mode == "native" {
							features := append([]string{}, agent.Features...)
							var old []string
							for _, feature := range features {
								if feature != core.AgentFeatureXHTTPTLS {
									old = append(old, feature)
								}
							}
							if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: old, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true}}}); err != nil {
								t.Fatal(err)
							}
							actor.call("POST", base+"/server-inbounds", body, http.StatusConflict, nil)
							if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: features, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true}}}); err != nil {
								t.Fatal(err)
							}
						}
						var saved configMutationResult
						actor.call("POST", base+"/server-inbounds", body, http.StatusOK, &saved)
						version = saved.Config.Version
						last = saved
						actor.call("POST", base+"/server-inbounds", body, http.StatusConflict, nil)
						claimed, err := db.ClaimTask(ctx, agent.ID)
						if err != nil || claimed == nil || claimed.ConfigID != saved.Config.ID || claimed.ConfigVersion != version || claimed.SharedInstance != shared {
							t.Fatalf("wrong execution snapshot: %+v %v", claimed, err)
						}
						if panelCertificate && (len(claimed.Certificates) != 1 || claimed.Certificates[0].ID != certificate.ID) {
							t.Fatal("missing authorized proxy certificate")
						}
						if mode == "native" {
							if len(claimed.TLSFrontends) != 0 {
								t.Fatal("native kept proxy")
							}
						} else {
							if len(claimed.TLSFrontends) != 1 {
								t.Fatal("missing proxy")
							}
							f := claimed.TLSFrontends[0]
							if panelCertificate && (!f.UsesPanelCertificate() || f.UsesACME() || f.AutomaticCertificate) {
								t.Fatal("panel certificate unexpectedly enabled ACME")
							}
							if f.Mode != mode || f.Tag != plan.Tag || f.StaticHTML != plan.TLSStaticHTML || f.BackendPort != plan.Port || f.PublicPort != plan.TLSPublicPort || f.LocalOnly {
								t.Fatal("wrong owned proxy")
							}
						}
						result := core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}
						if shared {
							result.TrafficSettled = true
							result.InstanceStatus = &core.SharedInstanceStatus{Engine: engine, ShareID: claimed.SharedTrafficID, ConfigID: claimed.ConfigID, ConfigVersion: version, AppliedTaskID: claimed.ID, TaskID: claimed.ID, Status: "active", ObservedAt: time.Now().UTC()}
						}
						if err := db.CompleteTask(ctx, agent.ID, claimed.ID, result); err != nil {
							t.Fatal(err)
						}
						var workspace struct{ Inbounds []serverconfig.Input }
						actor.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
						if len(workspace.Inbounds) != 1 {
							t.Fatal("lost inbound")
						}
						got := workspace.Inbounds[0]
						if panelCertificate && got.PanelCertificateID != certificate.ID {
							t.Fatal("proxy panel certificate lost on reopen")
						}
						if (got.TLSMode != mode && !(mode == "native" && got.TLSMode == "")) || got.TLSStaticHTML != plan.TLSStaticHTML || got.TransportPath != plan.TransportPath || got.Credential != plan.Credential {
							t.Fatal("mode save/reload changed inputs")
						}
						var entries []clientAccessEntry
						actor.call("GET", "/client-access?outbound_engine=xray", nil, http.StatusOK, &entries)
						if len(entries) != 1 || entries[0].AddressRequired || len(entries[0].Profiles) != 1 {
							t.Fatalf("domain-only export missing: %+v", entries)
						}
						profile := entries[0].Profiles[0].Profile
						uri, err := url.Parse(profile.URI)
						expectedPort := plan.TLSPublicPort
						if mode == "native" {
							expectedPort = plan.Port
						}
						if err != nil || uri.Hostname() != plan.TLSServerName || uri.Port() != fmt.Sprint(expectedPort) || uri.Query().Get("sni") != plan.TLSServerName || uri.Query().Get("path") != plan.TransportPath || strings.Contains(profile.Xray, `"allowInsecure": true`) {
							t.Fatal("private/wrong public export", profile.URI)
						}
						foreign := recipient
						if shared {
							foreign = owner
						}
						foreign.call("GET", "/configs/"+saved.Config.ID+"/revisions", nil, http.StatusNotFound, nil)
						admin.call("GET", "/configs/"+saved.Config.ID+"/revisions", nil, http.StatusOK, nil)
					}
					if panelCertificate {
						pair, err := certificates.SelfSigned([]string{plan.TLSServerName}, "ecdsa256", 100)
						if err != nil {
							t.Fatal(err)
						}
						owner.call("POST", "/certificates/"+certificate.ID+"/replace", pair, 200, &certificate)
						if err = db.RunCertificateMaintenance(ctx); err != nil {
							t.Fatal(err)
						}
						renewal, err := db.ClaimTask(ctx, agent.ID)
						if err != nil || renewal == nil || len(renewal.Certificates) != 1 || renewal.Certificates[0].Version != 2 || len(renewal.TLSFrontends) != 1 || !renewal.TLSFrontends[0].UsesPanelCertificate() {
							t.Fatalf("proxy renewal did not reach applied instance: %v", err)
						}
						result := core.TaskResultRequest{LeaseID: renewal.LeaseID, Success: true}
						if shared {
							result.TrafficSettled = true
							result.InstanceStatus = &core.SharedInstanceStatus{Engine: engine, ShareID: renewal.SharedTrafficID, ConfigID: renewal.ConfigID, ConfigVersion: renewal.ConfigVersion, AppliedTaskID: renewal.ID, TaskID: renewal.ID, Status: "active", ObservedAt: time.Now().UTC()}
						}
						if err = db.CompleteTask(ctx, agent.ID, renewal.ID, result); err != nil {
							t.Fatal(err)
						}
					}
					deletion := map[string]any{"operation": "delete", "original_tag": plan.Tag, "expected_version": version, "intent": "deploy"}
					var old []string
					for _, feature := range agent.Features {
						if feature != core.AgentFeatureManagedTLSCertificates {
							old = append(old, feature)
						}
					}
					if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: old, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true}}}); err != nil {
						t.Fatal(err)
					}
					actor.call("POST", base+"/server-inbounds", deletion, 409, nil)
					if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true}}}); err != nil {
						t.Fatal(err)
					}
					actor.call("POST", base+"/server-inbounds", deletion, 200, &last)
					removal, err := db.ClaimTask(ctx, agent.ID)
					if err != nil || removal == nil || len(removal.TLSFrontends) != 0 || len(removal.Certificates) != 0 {
						t.Fatalf("deleted inbound retained proxy certificate: %v", err)
					}
					result := core.TaskResultRequest{LeaseID: removal.LeaseID, Success: true}
					if shared {
						result.TrafficSettled = true
						result.InstanceStatus = &core.SharedInstanceStatus{Engine: engine, ShareID: removal.SharedTrafficID, ConfigID: removal.ConfigID, ConfigVersion: removal.ConfigVersion, AppliedTaskID: removal.ID, TaskID: removal.ID, Status: "active", ObservedAt: time.Now().UTC()}
					}
					if err = db.CompleteTask(ctx, agent.ID, removal.ID, result); err != nil {
						t.Fatal(err)
					}
					if panelCertificate {
						owner.call("DELETE", "/certificates/"+certificate.ID, nil, 204, nil)
					}

				})
			}
		}
	}
}
