package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

var migrationTaskID = regexp.MustCompile(`^tsk_[0-9a-f]{16}$`)

type migrationServiceSnapshot struct {
	Mode, Service, Content, Status, Enabled string
	Exists                                  bool
}
type tlsMigrationJournal struct {
	Operation      core.TLSMigration
	Previous, Next tlsDeployments
	Snapshots      []migrationServiceSnapshot
	Result         core.TLSMigrationResult
}

func (m *tlsFrontendManager) migrationFile(name string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return m.writeConfig(filepath.Join(m.root, name), string(raw), true)
}
func checkMigrationOperation(op *core.TLSMigration) error {
	if op == nil || !migrationTaskID.MatchString(op.ID) || op.Port < 1 || op.Port > 65535 || !slices.Contains([]string{"nginx", "caddy"}, op.Mode) || len(op.Routes) == 0 || len(op.Routes) > 256 {
		return errors.New("invalid TLS migration")
	}
	return nil
}
func (m *tlsFrontendManager) migrationRoutes(op core.TLSMigration, before tlsDeployments) (tlsDeployments, error) {
	next := tlsDeployments{}
	count := 0
	for service, d := range before {
		d.Frontends = slices.Clone(d.Frontends)
		for i, f := range d.Frontends {
			if f.PublicPort != op.Port {
				continue
			}
			if d.Stopped || d.PendingConfig != "" || d.NativeHandoff != nil || f.LocalOnly || !f.UsesPanelCertificate() {
				return nil, errors.New("unconfirmed frontend binding")
			}
			matches := 0
			for _, r := range op.Routes {
				spec := DefaultSpecsForServiceManager(m.services.Kind())[r.Engine]
				if r.InstanceID != "" {
					var err error
					spec, err = sharedInstanceSpec(r.Engine, spec, r.InstanceID, m.services.Kind())
					if err != nil {
						return nil, err
					}
				}
				suffix := fmt.Sprintf("/panel-certificates/%s/v%d/cert.pem", r.CertificateID, r.CertificateVersion)
				if spec.Service != service || f.Tag != r.Tag || f.Path != r.Path || f.Domain != r.Domain || f.BackendPort != r.BackendPort || f.Transport != r.Transport || f.Mode != r.From || !strings.HasSuffix(f.CertificatePath, suffix) {
					continue
				}
				matches++
				d.Frontends[i].Mode = op.Mode
				d.Frontends[i].SharedIngressGeneration = r.GrantGeneration
			}
			if matches != 1 {
				return nil, errors.New("port group changed")
			}
			count++
		}
		next[service] = d
	}
	if count != len(op.Routes) {
		return nil, errors.New("incomplete port group")
	}
	_, err := activeTLSFrontends(next)
	return next, err
}

// Source listeners must be owned by the managed process on every address family.
// Preserve non-standard active proxy files by refusing a destructive regeneration.
func (m *tlsFrontendManager) checkMigrationSource(ctx context.Context, port int, active []core.TLSFrontend, snapshots []tlsRuntimeSnapshot) error {
	for _, snapshot := range snapshots {
		if snapshot.status != "active" {
			continue
		}
		expected := renderNginxFrontends(active, m.root, m.state, m.nginxHTTPRouteAvailable())
		if snapshot.mode == "caddy" {
			var err error
			expected, err = renderCaddyWithFallback(active, m.state, nil, m.root)
			if err != nil {
				return err
			}
		}
		if expected != snapshot.content {
			return errors.New("active proxy configuration differs from managed routes")
		}
	}
	for _, f := range active {
		if f.PublicPort == port {
			service, _, _ := m.serviceDefinition(f.Mode, false)
			pid, err := managedServiceProcessPID(ctx, m.services, service)
			if err != nil {
				return err
			}
			return processOwnsTCPPort(pid, port)
		}
	}
	return errors.New("source listener unavailable")
}

func (m *tlsFrontendManager) verifyMigration(ctx context.Context, j *tlsMigrationJournal, deployments tlsDeployments, recovery bool) bool {
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return false
	}
	good := true
	for i, r := range j.Operation.Routes {
		spec := DefaultSpecsForServiceManager(m.services.Kind())[r.Engine]
		if r.InstanceID != "" {
			spec, err = sharedInstanceSpec(r.Engine, spec, r.InstanceID, m.services.Kind())
			if err != nil {
				return false
			}
		}
		state := "unknown"
		src, e := readConfigurationFile(spec.ConfigPath)
		pid, pidErr := managedServiceProcessPID(ctx, m.services, spec.Service)
		if e == nil && pidErr == nil {
			for _, f := range deployments[spec.Service].Frontends {
				if f.Tag != r.Tag {
					continue
				}
				for _, p := range serverconfig.ParseAll(r.Engine, string(src)) {
					if p.Tag != r.Tag {
						continue
					}
					binary := DefaultSpecs()[core.EngineXray].Binary
					if e = verifyMigrationRoute(ctx, binary, pid, f, p, &identity); e == nil {
						state = "succeeded"
					} else {
						state = "failed"
					}
					break
				}
			}
		}
		if recovery {
			j.Result.Paths[i].Recovery = state
		} else {
			j.Result.Paths[i].Candidate = state
		}
		good = good && state == "succeeded"
	}
	return good
}

const migrationPending = "tls-migration.pending"

func (m *tlsFrontendManager) recoverTLSMigration(ctx context.Context) error {
	data, err := readConfigurationFile(filepath.Join(m.root, migrationPending))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var j tlsMigrationJournal
	if err = json.Unmarshal([]byte(data), &j); err != nil || checkMigrationOperation(&j.Operation) != nil || len(j.Result.Paths) != len(j.Operation.Routes) {
		return errors.New("invalid pending TLS migration")
	}
	var receipt core.TLSMigrationResult
	if raw, e := os.ReadFile(filepath.Join(m.root, j.Operation.ID+".result")); e == nil && json.Unmarshal(raw, &receipt) == nil && receipt.State == "succeeded" {
		return os.Remove(filepath.Join(m.root, migrationPending))
	}
	return m.restoreTLSMigration(ctx, &j)
}
func (m *tlsFrontendManager) restoreTLSMigration(ctx context.Context, j *tlsMigrationJournal) error {
	var snapshots []tlsRuntimeSnapshot
	for _, s := range j.Snapshots {
		snapshots = append(snapshots, tlsRuntimeSnapshot{s.Mode, s.Service, s.Content, s.Status, s.Enabled, s.Exists})
	}
	j.Result.Step = "restore"
	j.Result.State = "recovery_incomplete"
	current, err := m.load()
	if err == nil {
		equal := func(a, b tlsDeployments) bool {
			x, _ := json.Marshal(a)
			y, _ := json.Marshal(b)
			return bytes.Equal(x, y)
		}
		if !equal(current, j.Previous) && !equal(current, j.Next) {
			err = errors.New("runtime manifest changed independently")
		}
	}
	if err == nil {
		err = m.restoreRuntime(ctx, j.Previous, snapshots)
	}
	if err == nil {
		err = m.migrationFile("deployments.json", j.Previous)
	}
	if err == nil && m.verifyMigration(ctx, j, j.Previous, true) {
		j.Result.State = "rolled_back"
	} else if err == nil {
		err = errors.New("original path verification incomplete")
	}
	if e := m.migrationFile(j.Operation.ID+".result", j.Result); e != nil {
		return e
	}
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(m.root, migrationPending))
}
func (m *tlsFrontendManager) migrateTLS(ctx context.Context, op *core.TLSMigration) error {
	if err := checkMigrationOperation(op); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(m.root, op.ID+".result")); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	j := tlsMigrationJournal{Operation: *op, Result: core.TLSMigrationResult{State: "failed", Step: "preflight", Paths: make([]core.TLSRouteVerification, len(op.Routes))}}
	for i := range j.Result.Paths {
		j.Result.Paths[i] = core.TLSRouteVerification{Candidate: "unknown", Recovery: "unknown"}
	}
	fail := func() error { return m.migrationFile(op.ID+".result", j.Result) }
	var err error
	if j.Previous, err = m.load(); err != nil {
		return fail()
	}
	if j.Next, err = m.migrationRoutes(*op, j.Previous); err != nil {
		return fail()
	}
	old, e := activeTLSFrontends(j.Previous)
	next, err := activeTLSFrontends(j.Next)
	if e != nil || err != nil || m.checkPorts(old, next) != nil || m.validateConfigs(ctx, next) != nil {
		return fail()
	}
	if !m.verifyMigration(ctx, &j, j.Previous, true) {
		return fail()
	}
	for i := range j.Result.Paths {
		j.Result.Paths[i].Recovery = "unknown"
	}
	snapshots, err := m.snapshotRuntime(ctx)
	if err != nil || m.checkMigrationSource(ctx, op.Port, old, snapshots) != nil {
		return fail()
	}
	for _, s := range snapshots {
		j.Snapshots = append(j.Snapshots, migrationServiceSnapshot{s.mode, s.service, s.content, s.status, s.enabled, s.exists})
	}
	j.Result.Step = "handoff"
	if err = m.migrationFile(migrationPending, j); err != nil {
		return err
	}
	err = m.sync(ctx, j.Next)
	if err == nil {
		j.Result.Step = "verify"
		if !m.verifyMigration(ctx, &j, j.Next, false) {
			err = errors.New("path verification failed")
		}
	}
	if err == nil {
		err = m.migrationFile("deployments.json", j.Next)
	}
	if err != nil {
		recovery, cancel := context.WithTimeout(context.Background(), core.ManagedTLSOperationTimeout)
		defer cancel()
		return m.restoreTLSMigration(recovery, &j)
	}
	j.Result.State = "succeeded"
	j.Result.Step = "complete"
	if err = m.migrationFile(op.ID+".result", j.Result); err != nil {
		return err
	}
	return os.Remove(filepath.Join(m.root, migrationPending))
}
func (e *Executor) runTLSMigration(ctx context.Context, task core.Task) (*core.TLSMigrationResult, error) {
	op := task.TLSMigration
	if err := checkMigrationOperation(op); err != nil || op.ID != task.ID {
		return nil, errors.New("invalid migration task")
	}
	kind := e.serviceManager().Kind()
	req := tlsFrontendRequest{Action: task.Action, Migration: op, Service: DefaultSpecsForServiceManager(kind)[core.EngineSingBox].Service, Kind: kind, Deadline: tlsOperationDeadline(ctx)}
	err := e.runTLSFrontendRequest(ctx, req)
	data, readErr := readConfigurationFile(filepath.Join(tlsFrontendRoot, op.ID+".result"))
	var result core.TLSMigrationResult
	if readErr != nil || json.Unmarshal([]byte(data), &result) != nil {
		return nil, errors.New("migration receipt unavailable")
	}
	if result.State != "succeeded" {
		err = errors.New("migration did not complete")
	}
	return &result, err
}
