package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSharedPortModesAPI(t *testing.T) {
	for _, sender := range []string{"owner", "administrator"} {
		t.Run(sender, func(t *testing.T) {
			db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "ports-api")
			sharingPath := "/agents/" + agent.ID + "/sharing"
			allocationPath := "/users/" + recipient.userID + "/agent-access"
			save := func(ports []int, status int) core.AgentShare {
				t.Helper()
				if sender == "owner" {
					var current core.AgentSharing
					owner.call("GET", sharingPath, nil, http.StatusOK, &current)
					input := core.AgentSharingRequest{Revision: current.Revision,
						Shares: []core.AgentSharingRecipient{{Username: "bob", Engines: []core.Engine{core.EngineMihomo}, Ports: ports, LimitBytes: 1000}}}
					owner.call("PUT", sharingPath, input, status, nil)
					if status == http.StatusOK {
						owner.call("PUT", sharingPath, input, http.StatusConflict, nil)
					}
				} else {
					var current core.AgentAccess
					admin.call("GET", allocationPath, nil, http.StatusOK, &current)
					input := core.AgentAccessRequest{Revision: current.Revision, Isolated: true,
						Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: ports, LimitBytes: 1000}}}
					admin.call("PUT", allocationPath, input, status, nil)
					if status == http.StatusOK {
						admin.call("PUT", allocationPath, input, http.StatusConflict, nil)
					}
				}
				var access core.AgentAccess
				recipient.call("GET", "/agent-access", nil, http.StatusOK, &access)
				if len(access.Shares) != 1 {
					t.Fatalf("recipient lost its allocation: %+v", access)
				}
				return access.Shares[0]
			}
			blank := save(nil, http.StatusOK)
			if len(blank.Ports) != 0 || blank.Status != core.AgentSharePending {
				t.Fatalf("blank unexpectedly granted ports or consent: %+v", blank)
			}
			unrestricted := save([]int{0}, http.StatusOK)
			if !slices.Equal(unrestricted.Ports, []int{0}) {
				t.Fatalf("API did not preserve unrestricted mode: %+v", unrestricted)
			}
			recipient.call("POST", "/agent-access/"+blank.ID+"/response",
				core.AgentShareResponseRequest{Revision: blank.InvitationRevision, Decision: "accept"}, http.StatusConflict, nil)
			recipient.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus}, http.StatusNotFound, nil)
			recipient.call("PUT", sharingPath, core.AgentSharingRequest{Revision: 1}, http.StatusNotFound, nil)
			for _, invalid := range [][]int{{0, 21001}, {0, 0}, {10085}, {10086}, {65536}} {
				after := save(invalid, http.StatusBadRequest)
				if !slices.Equal(after.Ports, []int{0}) || after.InvitationRevision != unrestricted.InvitationRevision {
					t.Fatalf("invalid edit changed port authority: %+v", after)
				}
			}
			finite := save([]int{21002, 21001}, http.StatusOK)
			if !slices.Equal(finite.Ports, []int{21001, 21002}) {
				t.Fatalf("explicit allocation no longer round-trips: %+v", finite)
			}
			blank = save([]int{}, http.StatusOK)
			recipient.call("POST", "/agent-access/"+blank.ID+"/response",
				core.AgentShareResponseRequest{Revision: blank.InvitationRevision, Decision: "accept"}, http.StatusOK, nil)
			recipient.call("PUT", sharingPath, core.AgentSharingRequest{Revision: 1}, http.StatusForbidden, nil)
			var config core.Config
			recipient.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", configScopeAPIConfig(t, 21001), http.StatusOK, &config)
			request := core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID}
			for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
				request.Action = action
				recipient.call("POST", "/tasks", request, http.StatusConflict, nil)
			}
			unrestricted = save([]int{0}, http.StatusOK)
			if unrestricted.Status != core.AgentShareAccepted {
				t.Fatal("port mode edit lost existing acceptance")
			}
			request.Action = core.ActionValidate
			var validation core.Task
			recipient.call("POST", "/tasks", request, http.StatusCreated, &validation)
			completeConfigScopeAPITask(t, ctx, db, validation)
			if policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID); err != nil || len(policies) != 0 {
				t.Fatalf("validation created runtime accounting: %+v %v", policies, err)
			}
			request.Action = core.ActionDeploy
			var deployment core.Task
			recipient.call("POST", "/tasks", request, http.StatusCreated, &deployment)
			completeConfigScopeAPITask(t, ctx, db, deployment)
			policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
			if err != nil || len(policies) != 1 || policies[0].Port != 21001 || policies[0].SharedQuota == nil {
				t.Fatalf("deployment did not bind its real listener: %+v %v", policies, err)
			}
			if after := save(nil, http.StatusConflict); !slices.Equal(after.Ports, []int{0}) {
				t.Fatalf("unsafe release changed port mode: %+v", after)
			}
			var stop core.Task
			owner.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStop}, http.StatusCreated, &stop)
			completeConfigScopeAPITask(t, ctx, db, stop)
			if after := save(nil, http.StatusOK); len(after.Ports) != 0 {
				t.Fatalf("settled clear did not persist an unallocated scope: %+v", after)
			}
			recipient.call("POST", "/tasks", request, http.StatusConflict, nil)
		})
	}
}
