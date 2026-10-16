package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestEditedRetryIsAtomicAndVersioned(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	config, err := db.CreateConfig(ctx, core.Config{Name: "retry", Engine: core.EngineMihomo, Content: "listeners: [{name: a, port: 1080, type: socks}]\nrules: ['MATCH,DIRECT']\n"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionValidate, Engine: config.Engine, ConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.pool.Exec(ctx, `UPDATE tasks SET status='failed',error='fixture failure',finished_at=now(),config_content=NULL WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	_, current, err := db.RetryEditor(ctx, original.ID)
	if err != nil || current.ID != config.ID || current.Version != 1 {
		t.Fatalf("editor: %+v %v", current, err)
	}
	changed := strings.Replace(config.Content, "1080", "1081", 1)
	saved, retry, err := db.SaveEditedRetry(ctx, original.ID, changed, 1)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 2 || retry.ConfigVersion != 2 || retry.ConfigID != config.ID || retry.Action != core.ActionValidate || retry.ID == original.ID {
		t.Fatalf("incorrect retry: %+v %+v", saved, retry)
	}
	if _, _, err = db.SaveEditedRetry(ctx, original.ID, config.Content, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale edit accepted: %v", err)
	}
	if _, _, err = db.RetryEditor(WithConfigScope(ctx, "other-user", false), original.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign configuration exposed: %v", err)
	}
	if _, err = db.pool.Exec(ctx, `CREATE FUNCTION reject_edited_retry() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test queue failure'; END $$; CREATE TRIGGER reject_edited_retry BEFORE INSERT ON tasks FOR EACH ROW EXECUTE FUNCTION reject_edited_retry()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.SaveEditedRetry(ctx, original.ID, config.Content, 2); err == nil {
		t.Fatal("queue failure accepted")
	}
	revisions, err := db.ListConfigRevisions(ctx, config.ID, 10)
	if err != nil || len(revisions) != 2 || revisions[0].Version != 2 {
		t.Fatalf("failed queue persisted edit: %+v %v", revisions, err)
	}
}

func TestManagedTLSExecutionSnapshotAndDowngrade(t *testing.T) {
	for _, variant := range []string{"nginx", "caddy", "caddy-h2"} {
		mode := strings.TrimSuffix(variant, "-h2")
		t.Run(variant, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			agent, _ := enrollTaskTestAgent(t, ctx, db)
			if _, err := db.pool.Exec(ctx, `UPDATE agents SET capabilities='["sing-box"]',supported_capabilities='["sing-box"]' WHERE id=$1`, agent.ID); err != nil {
				t.Fatal(err)
			}
			protocolKey := "vless-ws-tls"
			if variant == "caddy-h2" {
				protocolKey = "vless-h2-tls"
			}
			protocol, _ := serverconfig.FindProtocol(core.EngineSingBox, protocolKey)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.TLSMode = mode
			if mode == "caddy" {
				input.TLSACMEEmail = "certs@example.test"
			}
			input.TLSServerName = "tls.example.test"
			input.TLSPublicPort = 8443
			content, err := serverconfig.Generate(core.EngineSingBox, input)
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := serverconfig.MarshalClientMetadataWithPrevious(input, "")
			if err != nil {
				t.Fatal(err)
			}
			options := ConfigMutationOptions{Action: core.ActionDeploy, ClientMetadata: &ConfigClientMetadataMutation{Tag: input.Tag, Content: metadata}}
			config := core.Config{AgentID: agent.ID, Engine: core.EngineSingBox, Name: "managed TLS", Content: content}
			if _, _, err = db.SaveAgentConfigAndTask(ctx, config, 0, options); !errors.Is(err, ErrConflict) {
				t.Fatalf("old agent accepted: %v", err)
			}
			if _, err = db.AgentConfig(ctx, agent.ID, core.EngineSingBox); !errors.Is(err, ErrNotFound) {
				t.Fatalf("old-agent rejection persisted config: %v", err)
			}
			if _, err = db.pool.Exec(ctx, `UPDATE agents SET features=features||'["managed-tls-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
				t.Fatal(err)
			}
			if variant == "caddy-h2" {
				if _, _, err = db.SaveAgentConfigAndTask(ctx, config, 0, options); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "managed-tls-h2-v1") {
					t.Fatalf("old agent accepted H2: %v", err)
				}
				if _, err = db.pool.Exec(ctx, `UPDATE agents SET features=features||'["managed-tls-h2-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "caddy" {
				if _, _, err = db.SaveAgentConfigAndTask(ctx, config, 0, options); !errors.Is(err, ErrConflict) {
					t.Fatalf("old agent silently accepted ACME fallback: %v", err)
				}
				if _, err = db.pool.Exec(ctx, `UPDATE agents SET features=features||'["managed-tls-acme-fallback-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
					t.Fatal(err)
				}
			}
			saved, task, err := db.SaveAgentConfigAndTask(ctx, config, 0, options)
			if err != nil {
				t.Fatal(err)
			}
			if len(task.TLSFrontends) > 0 {
				t.Fatal("panel task response leaks private frontend")
			}
			var stored string
			if err = db.pool.QueryRow(ctx, `SELECT config_content FROM tasks WHERE id=$1`, task.ID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stored, "PRIVATE KEY") || strings.Contains(stored, input.Credential) {
				t.Fatal("snapshot is not encrypted")
			}
			claimed, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || claimed == nil {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			if claimed.ConfigContent != saved.Content || len(claimed.TLSFrontends) != 1 || claimed.TLSFrontends[0].PublicPort != 8443 || mode == "nginx" && claimed.TLSFrontends[0].PrivateKey == "" || claimed.TLSFrontends[0].ACMEEmail != input.TLSACMEEmail {
				t.Fatalf("lost execution snapshot: %+v", claimed)
			}
			feature := core.AgentFeatureManagedTLS
			if mode == "caddy" {
				feature = core.AgentFeatureACMEFallback
			}
			if variant == "caddy-h2" {
				feature = core.AgentFeatureManagedTLSH2
			}
			if _, err = db.pool.Exec(ctx, `UPDATE agents SET features=features-$2 WHERE id=$1`, agent.ID, feature); err != nil {
				t.Fatal(err)
			}
			running, err := db.RunningTask(ctx, agent.ID)
			if err != nil || running != nil {
				t.Fatalf("downgraded agent resumed: %+v %v", running, err)
			}
			var status, errorText string
			var snapshot, lease *string
			if err = db.pool.QueryRow(ctx, `SELECT status,COALESCE(error,''),config_content,lease_id FROM tasks WHERE id=$1`, task.ID).Scan(&status, &errorText, &snapshot, &lease); err != nil {
				t.Fatal(err)
			}
			if status != "failed" || snapshot != nil || lease != nil || !strings.Contains(errorText, feature) {
				t.Fatalf("unsafe downgrade cleanup: %s %s", status, errorText)
			}

		})
	}
}
