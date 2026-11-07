package store

import (
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestNodeAddressReplacesLegacyDefaultsOnly(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	profile := core.ClientProfileNameLabel(core.EngineMihomo, "::", 21001)
	profileAddress := core.ClientProfileAddressLabel(core.EngineMihomo, "::", 21001)
	profileMode := core.ClientProfileFamilyLabel(core.EngineMihomo, "::", 21001)
	for _, legacy := range []string{`{"client_address":"old.example"}`, `{"public_host":"legacy.example"}`, `{"public_ip":"9.9.9.9"}`, `{"client_address":"old.example","public_host":"legacy.example","public_ip":"9.9.9.9"}`} {
		for _, address := range []string{"", "new.example"} {
			if _, err := db.pool.Exec(ctx, `UPDATE agents SET labels=$2::jsonb || jsonb_build_object('client_name','keep','client_address_mode','ipv6','region_code','JP',$3::text,'port name',$4::text,'port.example',$5::text,'ipv4') WHERE id=$1`, agent.ID, legacy, profile, profileAddress, profileMode); err != nil {
				t.Fatal(err)
			}
			// Unrelated partial changes preserve legacy defaults.
			name := "keep"
			if err := db.SetAgentClientDetails(ctx, agent.ID, nil, &name); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := db.SetAgentClientAddress(ctx, agent.ID, address); err != nil {
					t.Fatal(err)
				}
				if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{}); err != nil {
					t.Fatal(err)
				}
				loaded, err := db.GetAgent(ctx, agent.ID)
				if err != nil {
					t.Fatal(err)
				}
				if loaded.Labels["client_address"] != address || loaded.Labels["public_host"] != "" || loaded.Labels["public_ip"] != "" {
					t.Fatalf("stale node defaults: %+v", loaded.Labels)
				}
				var stored map[string]string
				if err := db.pool.QueryRow(ctx, `SELECT labels FROM agents WHERE id=$1`, agent.ID).Scan(&stored); err != nil {
					t.Fatal(err)
				}
				for key, want := range map[string]string{"client_name": "keep", "client_address_mode": "ipv6", "region_code": "JP", profile: "port name", profileAddress: "port.example", profileMode: "ipv4"} {
					if stored[key] != want {
						t.Fatalf("lost %s: %+v", key, loaded.Labels)
					}
				}
			}
		}
	}
	// A legacy profile reset cannot clear node defaults either.
	address := "node.example"
	if err := db.SetAgentClientAddress(ctx, agent.ID, address); err != nil {
		t.Fatal(err)
	}
	empty := ""
	if err := db.SetAgentClientProfilePreferences(ctx, agent.ID, profile, &empty, nil, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.GetAgent(ctx, agent.ID)
	if err != nil || loaded.Labels["client_address"] != address {
		t.Fatalf("profile reset changed node: %+v %v", loaded.Labels, err)
	}
}
