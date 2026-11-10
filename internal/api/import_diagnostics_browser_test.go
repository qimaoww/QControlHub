package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// Real panel sessions and production API/store, with explicitly simulated
// Agent receipts. Real core execution is covered by the disposable-node tests.
func TestImportDiagnosticsBrowserSessions(t *testing.T) {
	if os.Getenv("QCH_CORE_INSTALL_BROWSER") != "1" {
		t.Skip("enable with QCH_CORE_INSTALL_BROWSER=1")
	}
	for _, mobile := range []bool{false, true} {
		t.Run(fmt.Sprintf("mobile=%t", mobile), func(t *testing.T) {
			db, ctx, _, owner, _ := newConfigScopeAPIFixtureWithin(t, 18*time.Minute)
			node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "Import boundary", core.EngineXray)
			features := append(node.Features, core.AgentFeatureImportIsolation, core.AgentFeatureImportDependencies)
			heartbeat := func(available bool) error {
				reason := ""
				if !available {
					reason = "检测到活动的 xray 服务，但配置源未通过受保护路径与真实内核校验：external service config permission denied"
				}
				sources := []core.ExistingCoreSource{}
				if available {
					sources = []core.ExistingCoreSource{{ID: strings.Repeat("a", 64), Service: "自定义 service-one.service", ConfigPath: "/srv/配置 one/config.json", Status: "active"}, {ID: strings.Repeat("b", 64), Service: "service-two.service", ConfigPath: "/opt/second/config.json", Status: "inactive"}}
				}
				return db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, Version: "26.2.6", ServiceStatus: "inactive", ExistingConfigAvailable: available, ExistingConfigUnsupportedReason: reason, ExistingSources: sources}}})
			}
			if err := heartbeat(false); err != nil {
				t.Fatal(err)
			}
			done, stopped := make(chan struct{}), make(chan struct{})
			defer func() { close(done); <-stopped }()
			workerErrors := make(chan error, 8)
			go func() {
				defer close(stopped)
				lastHeartbeat, importReady := time.Now(), false
				ticker := time.NewTicker(50 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
						if time.Since(lastHeartbeat) > 5*time.Second {
							_ = heartbeat(importReady)
							lastHeartbeat = time.Now()
						}
						task, err := db.ClaimTask(ctx, node.ID)
						if err != nil || task == nil {
							continue
						}
						result := core.TaskResultRequest{LeaseID: task.LeaseID, Success: true, Output: "托管配置已校验并部署。", TrafficSettled: true}
						switch task.Action {
						case core.ActionReadConfig:
							result.ImportSourceDigest = strings.Repeat("d", 64)
							result.ImportDependencies = []core.ImportDependency{{Field: "routing.rules[0].ip[0]", Source: "/srv/规则 集/地区 one.dat", Resolved: "/srv/archive/地区 one.dat", Destination: "/usr/local/lib/qagent/cores/imports/" + strings.Repeat("e", 64) + "/regions.dat", Kind: "input"}}
							result.Output = `{"inbounds":[{"tag":"external-source","listen":"127.0.0.1","port":21002,"protocol":"socks","settings":{"auth":"noauth"}}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`
						case core.ActionImportExisting, core.ActionValidate:
							result.Success = false
							result.Error = "[阶段:真实内核校验] fixture nested cause: permission denied\n" + strings.Repeat("中文多行诊断 long_without_spaces_\n", 4000) + "\nDIAGNOSTIC_END 原始错误保留；回滚成功。"
							result.Output = "执行输出\n" + strings.Repeat("output_with_no_spaces_", 6000) + "\nOUTPUT_END"
							if task.Action == core.ActionImportExisting {
								cfg, readErr := db.AgentConfig(store.WithConfigScope(ctx, owner.userID, false), node.ID, core.EngineXray)
								if readErr == nil {
									result.InstanceStatus = &core.SharedInstanceStatus{Engine: task.Engine, TaskID: task.ID, ConfigID: cfg.ID, ConfigVersion: cfg.Version, Status: "active", ObservedAt: time.Now().UTC()}
								}
							}
						}
						if !result.Success {
							b, _ := json.Marshal(core.TaskDiagnostic{Output: result.Output, Error: result.Error})
							digest := sha256.Sum256(b)
							result.DiagnosticDigest = hex.EncodeToString(digest[:])
							var chunks []string
							for len(b) > 0 {
								n := min(len(b), core.TaskDiagnosticChunkBytes)
								for !utf8.Valid(b[:n]) {
									n--
								}
								chunks = append(chunks, string(b[:n]))
								b = b[n:]
							}
							for index, content := range chunks {
								err = db.UploadTaskDiagnostic(ctx, node.ID, task.ID, core.TaskDiagnosticChunk{LeaseID: task.LeaseID, Index: index, Count: len(chunks), Content: content, Digest: result.DiagnosticDigest})
								if err != nil {
									break
								}
							}
							result.Error, result.Output = "真实内核校验失败；完整诊断见任务详情。", "输出摘要"
						}
						if err == nil {
							err = db.CompleteTask(ctx, node.ID, task.ID, result)
						}
						if err == nil && task.Action == core.ActionDeploy {
							importReady = true
							err = heartbeat(true)
						}
						if err != nil {
							select {
							case workerErrors <- err:
							default:
							}
						}
					}
				}
			}()
			frontendRoot := filepath.Join("..", "..", "frontend")
			mux := http.NewServeMux()
			mux.Handle("/api/", owner.handler)
			mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(frontendRoot))))
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				content, err := os.ReadFile(filepath.Join(frontendRoot, "index.html"))
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(bytes.Replace(content, []byte("</body>"), []byte(`<script type="module" src="/assets/import_diagnostics_browser_flow.mjs"></script></body>`), 1))
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			url := server.URL + "/?agent=" + node.ID
			if preview := os.Getenv("QCH_IMPORT_DIAGNOSTICS_PREVIEW"); preview != "" {
				if err := os.WriteFile(preview, []byte(url+"&preview=1"), 0600); err != nil {
					t.Fatal(err)
				}
				for {
					if _, err := os.Stat(preview + ".done"); err == nil {
						return
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(250 * time.Millisecond):
					}
				}
			}
			arguments := []string{filepath.Join(frontendRoot, "core_install_browser_session.mjs"), url}
			if mobile {
				arguments = append(arguments, "--mobile")
			}
			output, err := exec.CommandContext(ctx, "node", arguments...).CombinedOutput()
			if err != nil {
				t.Fatalf("browser: %v\n%s", err, output)
			}
			t.Log(string(output))
			select {
			case err := <-workerErrors:
				t.Fatal(err)
			default:
			}
		})
	}
}
