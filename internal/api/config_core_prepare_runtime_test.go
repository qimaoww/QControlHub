package api

import (
	"context"
	"crypto/sha256"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	agentexec "github.com/qimaoww/qcontrolhub/internal/agent"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// Opt in only inside a disposable node. Downloads, core validation,
// file publication and service verification use the production executor.
func TestConfigCorePrepareRealNode(t *testing.T) {
	if os.Getenv("QCH_CONFIG_PREPARE_REAL_NODE") != "1" {
		t.Skip("requires disposable node and official release downloads")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires disposable Docker node")
	}
	db, ctx, _, owner, recipient := newConfigScopeAPIFixtureWithin(t, 18*time.Minute)
	manager, err := agentexec.NewServiceManager(os.Getenv("QCH_CONFIG_PREPARE_MANAGER"))
	if err != nil {
		t.Fatal(err)
	}
	executor := &agentexec.Executor{Specs: agentexec.DefaultSpecsForServiceManager(manager.Kind()), Updater: agentexec.NewCoreUpdater(), Services: manager}
	serviceCommand := func(ctx context.Context, service, action string) ([]byte, error) {
		if manager.Kind() == agentexec.ServiceManagerOpenRC {
			if action == "is-active" {
				output, err := exec.CommandContext(ctx, "rc-service", service, "status").CombinedOutput()
				if err == nil {
					return []byte("active"), nil
				}
				return output, err
			}
			return exec.CommandContext(ctx, "rc-service", service, action).CombinedOutput()
		}
		return exec.CommandContext(ctx, "systemctl", action, service).CombinedOutput()
	}
	for _, engine := range core.AllEngines() {
		t.Run(string(engine), func(t *testing.T) {
			owner.t = t
			recipient.t = t
			node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "real-first-deploy-"+string(engine), engine)
			features := append(node.Features, core.AgentFeaturePresetAutoInstall, core.AgentFeatureSharedCorePrepare)
			heartbeat := func() {
				if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features, Runtime: executor.Runtime(ctx)}); err != nil {
					t.Fatal(err)
				}
			}
			heartbeat()
			spec := executor.Specs[engine]
			t.Cleanup(func() { _, _ = serviceCommand(context.Background(), spec.Service, "stop") })
			if _, err := os.Stat(spec.Binary); !os.IsNotExist(err) {
				t.Fatal("node must start with missing core", err)
			}
			protocol, _ := serverconfig.FindProtocol(engine, serverconfig.ProtocolSS2022)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Port = 28575
			content, err := serverconfig.Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			base := "/agents/" + node.ID + "/configs/" + string(engine)
			execute := func(result configMutationResult) {
				t.Helper()
				task, err := db.ClaimTask(ctx, node.ID)
				if err != nil || task == nil || task.ID != result.Task.ID {
					t.Fatalf("claim: %+v %v", task, err)
				}
				output, executionErr := executor.Execute(ctx, *task)
				receipt := core.TaskResultRequest{LeaseID: task.LeaseID, Success: executionErr == nil, Output: output, TrafficSettled: true}
				if task.SharedInstance && task.Action.ChangesInstance() {
					for _, observed := range executor.SharedInstanceStatuses(ctx) {
						if observed.Engine == task.Engine && observed.ShareID == task.SharedTrafficID {
							observed.TaskID = task.ID
							receipt.InstanceStatus = &observed
							break
						}
					}
				}
				if executionErr != nil {
					receipt.Error = executionErr.Error()
				}
				if err := db.CompleteTask(ctx, node.ID, task.ID, receipt); err != nil {
					t.Fatal(err)
				}
				reader := owner
				if task.SharedInstance {
					reader = recipient
				}
				var stored core.Task
				reader.call("GET", "/tasks/"+task.ID, nil, http.StatusOK, &stored)
				status := core.TaskSucceeded
				if executionErr != nil {
					status = core.TaskFailed
				}
				if stored.Status != status || stored.Output != output || stored.ConfigVersion != task.ConfigVersion || stored.SharedTrafficID != task.SharedTrafficID {
					t.Fatalf("task API disagrees with execution: %+v", stored)
				}
				heartbeat()
				if executionErr != nil {
					t.Fatalf("real %s execution failed: %v\n%s", engine, executionErr, output)
				}
				t.Logf("%s: real install/validation/deployment receipt succeeded", engine)
			}
			var result configMutationResult
			if os.Getenv("QCH_CONFIG_PREPARE_ENTRY") == "shared" {
				grantConfigScopeAPIAgent(t, ctx, db, node, recipient, input.Port)
				recipient.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "intent": "deploy", "install_if_missing": true, "input": input}, http.StatusOK, &result)
				execute(result)
				if !result.Task.SharedInstance || result.Task.SharedTrafficID == "" {
					t.Fatal("preparation did not target a private instance")
				}
				if _, err := os.Stat(spec.ConfigPath); !os.IsNotExist(err) {
					t.Fatal("shared preparation published a main configuration", err)
				}
				if executor.Runtime(ctx)[engine].ServiceStatus == "active" {
					t.Fatal("shared preparation activated main service")
				}
				service := privateService(spec.Service, result.Task.SharedTrafficID, manager.Kind())
				t.Cleanup(func() { _, _ = serviceCommand(context.Background(), service, "stop") })
				if output, err := serviceCommand(ctx, service, "is-active"); err != nil || strings.TrimSpace(string(output)) != "active" {
					t.Fatalf("private service is not running: %v %s", err, output)
				}
				// Preparing a private instance can be the first installation
				// on this node. A later main deployment reuses that binary and
				// safely creates its own unit while the private service runs.
				binary, err := os.ReadFile(spec.Binary)
				if err != nil {
					t.Fatal(err)
				}
				input.Port = 28576
				input.Tag = "main-after-shared"
				owner.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "intent": "deploy", "input": input}, http.StatusOK, &result)
				execute(result)
				if executor.Runtime(ctx)[engine].ServiceStatus != "active" {
					t.Fatal("main deployment could not reuse shared preparation")
				}
				after, err := os.ReadFile(spec.Binary)
				if err != nil || sha256.Sum256(after) != sha256.Sum256(binary) {
					t.Fatal("main deployment replaced shared binary", err)
				}
				if output, err := serviceCommand(ctx, service, "is-active"); err != nil || strings.TrimSpace(string(output)) != "active" {
					t.Fatalf("main deployment affected private service: %v %s", err, output)
				}
				if output, err := serviceCommand(ctx, service, "stop"); err != nil {
					t.Fatalf("stop private instance: %v %s", err, output)
				}
				return
			}
			if os.Getenv("QCH_CONFIG_PREPARE_ENTRY") == "retry" {
				owner.call("POST", base+"/source", map[string]any{"name": "retry-source", "content": content, "version": 0, "intent": "validate", "install_if_missing": true}, http.StatusOK, &result)
				execute(result)
				listener, err := net.Listen("tcp", "0.0.0.0:28575")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				var archiveTask core.Task
				owner.call("POST", "/configs/"+result.Config.ID+"/tasks", map[string]any{"agent_id": node.ID, "action": "deploy", "expected_config_version": result.Config.Version, "expected_instance_id": "", "install_if_missing": true}, http.StatusCreated, &archiveTask)
				result.Task = archiveTask
				failed, err := db.ClaimTask(ctx, node.ID)
				if err != nil || failed == nil || failed.ID != result.Task.ID {
					t.Fatalf("claim failure task: %+v %v", failed, err)
				}
				output, executionErr := executor.Execute(ctx, *failed)
				if executionErr == nil {
					t.Fatalf("occupied listener reported successful deployment: %s", output)
				}
				if err := db.CompleteTask(ctx, node.ID, failed.ID, core.TaskResultRequest{LeaseID: failed.LeaseID, Error: executionErr.Error(), Output: output, TrafficSettled: true}); err != nil {
					t.Fatal(err)
				}
				installed, err := os.ReadFile(spec.Binary)
				if err != nil {
					t.Fatal("installation did not finish before deployment failure", err)
				}
				if _, err := os.Stat(spec.ConfigPath); !os.IsNotExist(err) {
					t.Fatal("failed first deployment retained an invalid configuration", err)
				}
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
				heartbeat()
				retry, err := db.RetryTask(store.WithConfigScope(ctx, owner.userID, false), failed.ID)
				if err != nil {
					t.Fatal(err)
				}
				if retry.ConfigVersion != failed.ConfigVersion || retry.ConfigID != failed.ConfigID {
					t.Fatal("retry changed immutable snapshot")
				}
				execute(configMutationResult{Config: result.Config, Task: retry})
				after, err := os.ReadFile(spec.Binary)
				if err != nil || sha256.Sum256(after) != sha256.Sum256(installed) {
					t.Fatal("retry replaced the prepared binary", err)
				}
				if executor.Runtime(ctx)[engine].ServiceStatus != "active" {
					t.Fatal("retry did not start the instance")
				}
				if output, err := serviceCommand(ctx, spec.Service, "stop"); err != nil {
					t.Fatalf("stop retry service: %v %s", err, output)
				}
				return
			}
			if os.Getenv("QCH_CONFIG_PREPARE_ENTRY") == "add" {
				owner.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "intent": "deploy", "install_if_missing": true, "input": input, "expected_instance_id": ""}, http.StatusOK, &result)
				execute(result)
				if executor.Runtime(ctx)[engine].ServiceStatus != "active" {
					t.Fatal("fresh inbound deployment is not running")
				}
				if output, err := serviceCommand(ctx, spec.Service, "stop"); err != nil {
					t.Fatalf("stop: %v %s", err, output)
				}
				return
			}

			// Source-only validation can prepare a binary, but must not publish/start.
			owner.call("POST", base+"/source", map[string]any{"name": "real-source", "content": content, "version": 0, "intent": "validate", "install_if_missing": true, "expected_instance_id": ""}, http.StatusOK, &result)
			execute(result)
			if _, err := os.Stat(spec.ConfigPath); !os.IsNotExist(err) {
				t.Fatalf("validate published main configuration: %v", err)
			}
			if output, _ := serviceCommand(ctx, spec.Service, "is-active"); strings.TrimSpace(string(output)) == "active" {
				t.Fatal("validation started service")
			}
			binary, err := os.ReadFile(spec.Binary)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(binary)
			// Install again through the deployment itself, with an already saved source.
			if err := os.Remove(spec.Binary); err != nil {
				t.Fatal(err)
			}
			// Deploy the saved source while deliberately retaining the missing heartbeat.
			if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: false}}}); err != nil {
				t.Fatal(err)
			}
			owner.call("POST", base+"/source", map[string]any{"name": "real-source", "content": result.Config.Content, "version": result.Config.Version, "intent": "deploy", "install_if_missing": true, "expected_instance_id": ""}, http.StatusOK, &result)
			execute(result)
			if executor.Runtime(ctx)[engine].ServiceStatus != "active" {
				t.Fatal("successful receipt without running service")
			}
			after, err := os.ReadFile(spec.Binary)
			if err != nil || sha256.Sum256(after) != digest {
				t.Fatal("deployment replaced installed binary")
			}
			// Already installed deployment keeps the release and binary bytes.
			owner.call("POST", base+"/source", map[string]any{"name": "real-source", "content": result.Config.Content, "version": result.Config.Version, "intent": "deploy", "expected_instance_id": ""}, http.StatusOK, &result)
			execute(result)
			after, _ = os.ReadFile(spec.Binary)
			if sha256.Sum256(after) != digest {
				t.Fatal("installed-core deployment changed version")
			}
			mainConfig, err := os.ReadFile(spec.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			// The shared instance receives its own target; the main service stays active.
			grantConfigScopeAPIAgent(t, ctx, db, node, recipient, 28576)
			input.Port = 28576
			input.Tag = "shared-first"
			recipient.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "intent": "deploy", "install_if_missing": true, "input": input}, http.StatusOK, &result)
			execute(result)
			retained, err := os.ReadFile(spec.ConfigPath)
			if err != nil || string(retained) != string(mainConfig) {
				t.Fatal("shared deployment changed main configuration")
			}
			if executor.Runtime(ctx)[engine].ServiceStatus != "active" {
				t.Fatal("shared deployment stopped main service")
			}
			after, _ = os.ReadFile(spec.Binary)
			if sha256.Sum256(after) != digest {
				t.Fatal("shared deployment replaced common binary")
			}
			for _, service := range []string{spec.Service, privateService(spec.Service, result.Task.SharedTrafficID, manager.Kind())} {
				if output, err := serviceCommand(context.Background(), service, "stop"); err != nil {
					t.Fatalf("stop %s: %v %s", service, err, output)
				}
			}
		})
	}
}

func privateService(base, instance, manager string) string {
	if manager == agentexec.ServiceManagerOpenRC {
		return base + "-" + instance
	}
	service := strings.TrimSuffix(base, ".service") + "@" + instance
	if manager == agentexec.ServiceManagerSystemd {
		service += ".service"
	}
	return service
}
