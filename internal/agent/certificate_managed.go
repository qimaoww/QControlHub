package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Only the fixed Caddy certificates subtree is inspected. Account keys and
// arbitrary paths are never candidates. os.Root contains all traversals;
// symlinks, hard links, special files and oversized reads are rejected.
func readManagedCertificateFile(root *os.Root, path string, limit int64) ([]byte, error) {
	for p := path; p != "."; p = filepath.Dir(p) {
		info, err := root.Lstat(p)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || !managedCertificateOwnerSafe(info) {
			return nil, errors.New("symlink in certificate storage")
		}
	}
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > limit || !managedCertificateFileSafe(info) {
		return nil, errors.New("unsafe certificate file")
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("certificate file changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("certificate file exceeds limit")
	}
	return data, nil
}

func (m *tlsFrontendManager) scanManagedCertificate(source core.ManagedCertificateSource, kind string) core.ManagedCertificateReport {
	result := core.ManagedCertificateReport{SourceID: source.ID, Error: "managed-certificate-unavailable"}
	if source.Engine != core.EngineSingBox && source.Engine != core.EngineXray {
		return result
	}
	names, err := certificates.Domains([]string{source.Domain})
	if err != nil || names[0] != source.Domain {
		return result
	}
	spec, err := managedCertificateSpec(source, kind)
	if err != nil {
		return result
	}
	tlsFrontendMutex.Lock()
	deployments, err := m.load()
	tlsFrontendMutex.Unlock()
	if err != nil {
		return result
	}
	confirmed := false
	for _, f := range deployments[spec.Service].Frontends {
		if f.Mode == "caddy" && f.Domain == source.Domain {
			confirmed = true
		}
	}
	if !confirmed {
		return result
	}
	// Confirm the issuer from the protected running project configuration and
	// Caddy's certificate metadata. A newer staging/foreign issuer tree must
	// not displace the certificate used by this managed automation policy.
	configurationRoot, err := os.OpenRoot(m.root)
	if err != nil {
		return result
	}
	configuration, err := readManagedCertificateFile(configurationRoot, "caddy.conf", 4<<20)
	configurationRoot.Close()
	if err != nil {
		return result
	}
	var configured struct {
		Apps struct {
			TLS struct {
				Automation struct {
					Policies []struct {
						Subjects []string `json:"subjects"`
						Issuers  []struct {
							Module string `json:"module"`
							CA     string `json:"ca"`
						} `json:"issuers"`
					} `json:"policies"`
				} `json:"automation"`
			} `json:"tls"`
		} `json:"apps"`
	}
	if json.Unmarshal(configuration, &configured) != nil {
		return result
	}
	issuers := map[string]bool{}
	for _, policy := range configured.Apps.TLS.Automation.Policies {
		for _, subject := range policy.Subjects {
			if subject == source.Domain {
				for _, issuer := range policy.Issuers {
					if issuer.Module == "acme" && (issuer.CA == letsEncryptDirectory || issuer.CA == zeroSSLDirectory) {
						issuers[issuer.CA] = true
					}
				}
			}
		}
	}
	if len(issuers) == 0 {
		return result
	}
	dir := filepath.Join(m.state, "caddy", "certificates")
	if err = validateProtectedDirectoryChain(filepath.Dir(m.state)); err != nil {
		return result
	}
	for p := dir; p != filepath.Dir(m.state); p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || !managedCertificateOwnerSafe(info) {
			return result
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return result
	}
	defer root.Close()
	count := 0
	var newest, newestEnd time.Time
	_ = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		count++
		if count > 4096 {
			return fs.SkipAll
		}
		parts := strings.Split(filepath.ToSlash(path), "/")
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() && len(parts) == 2 && parts[1] != source.Domain {
			return fs.SkipDir
		}
		if !entry.Type().IsRegular() || len(parts) != 3 || parts[1] != source.Domain || parts[2] != source.Domain+".crt" {
			return nil
		}
		metadata, e := readManagedCertificateFile(root, strings.TrimSuffix(path, ".crt")+".json", 64<<10)
		var meta struct {
			Issuer struct {
				CA string `json:"ca"`
			} `json:"issuer_data"`
		}
		if e != nil || json.Unmarshal(metadata, &meta) != nil || !issuers[meta.Issuer.CA] {
			return nil
		}
		cert, e := readManagedCertificateFile(root, path, 256<<10)
		if e != nil {
			return nil
		}
		key, e := readManagedCertificateFile(root, strings.TrimSuffix(path, ".crt")+".key", 32<<10)
		if e != nil {
			return nil
		}
		pair := certificates.Pair{Certificate: string(cert), PrivateKey: string(key)}
		info, e := certificates.Inspect(pair, time.Now())
		if e != nil || len(info.Domains) != 1 || info.Domains[0] != source.Domain {
			return nil
		}
		if newest.IsZero() || info.NotBefore.After(newest) || info.NotBefore.Equal(newest) && info.NotAfter.After(newestEnd) {
			newest, newestEnd = info.NotBefore, info.NotAfter
			result.Certificate = pair.Certificate
			result.PrivateKey = pair.PrivateKey
			result.Error = ""
		}
		return nil
	})
	return result
}

func (c *Client) syncManagedCertificates(ctx context.Context, sources []core.ManagedCertificateSource, outgoing chan<- core.WireMessage) {
	if len(sources) > 128 {
		return
	}
	for _, source := range sources {
		if ctx.Err() != nil {
			return
		}
		report := c.executor.readManagedCertificate(ctx, source)
		select {
		case outgoing <- core.WireMessage{Type: core.WireManagedCertificateReport, ManagedCertificateReport: &report}:
		case <-ctx.Done():
			return
		}
	}
}

func managedCertificateSpec(source core.ManagedCertificateSource, kind string) (EngineSpec, error) {
	if source.Engine != core.EngineSingBox && source.Engine != core.EngineXray {
		return EngineSpec{}, errors.New("invalid managed certificate engine")
	}
	spec := DefaultSpecsForServiceManager(kind)[source.Engine]
	if source.ShareID != "" {
		return sharedInstanceSpec(source.Engine, spec, source.ShareID, kind)
	}
	return spec, nil
}

// The long-running Agent intentionally lacks DAC override. A short-lived,
// read-only helper can read Caddy's core-owned 0700 store. Its private response
// file never enters task output, stderr, a journal, or ordinary heartbeats.
func (e *Executor) readManagedCertificate(ctx context.Context, source core.ManagedCertificateSource) core.ManagedCertificateReport {
	failure := core.ManagedCertificateReport{SourceID: source.ID, Error: "managed-certificate-unavailable"}
	cleanupManagedCertificateResponses()
	kind := e.serviceManager().Kind()
	spec, err := managedCertificateSpec(source, kind)
	if err != nil {
		return failure
	}
	name, err := writeTLSFrontendRequest(tlsFrontendRequest{Service: spec.Service, Kind: kind, SyncSource: &source})
	if err != nil {
		return failure
	}
	response := filepath.Join(tlsFrontendRequests, name+".result")
	defer os.Remove(filepath.Join(tlsFrontendRequests, name))
	defer os.Remove(response)
	executable, err := os.Executable()
	if err != nil {
		return failure
	}
	executable, err = ipQualityExecutable(executable)
	if err != nil {
		return failure
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	if kind == ServiceManagerOpenRC {
		_, err = runIPQualityCommand(ctx, executable, []string{"manage-tls", name})
	} else {
		var runner string
		runner, err = ipQualityExecutable("/usr/bin/systemd-run")
		if err != nil {
			return failure
		}
		unit := "qagent-cert-read-" + strings.TrimSuffix(name, ".json") + ".service"
		_, err = runIPQualityCommand(ctx, runner, managedCertificateHelperArguments(executable, unit, name))
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = run(cleanup, e.serviceManager().executable, "stop", unit)
	}
	if err != nil {
		return failure
	}
	root, err := os.OpenRoot(tlsFrontendRequests)
	if err != nil {
		return failure
	}
	defer root.Close()
	info, err := root.Lstat(name + ".result")
	if err != nil || info.Mode().Perm()&0077 != 0 || validateOwner(info, "certificate response") != nil {
		return failure
	}
	data, err := readManagedCertificateFile(root, name+".result", 320<<10)
	if err != nil {
		return failure
	}
	var report core.ManagedCertificateReport
	if json.Unmarshal(data, &report) != nil || report.SourceID != source.ID {
		return failure
	}
	return report
}

func managedCertificateHelperArguments(executable, unit, name string) []string {
	return []string{"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec", "--unit=" + unit,
		"--property=User=root", "--property=Group=root", "--property=UMask=0077", "--property=NoNewPrivileges=yes",
		"--property=CapabilityBoundingSet=CAP_DAC_READ_SEARCH", "--property=AmbientCapabilities=CAP_DAC_READ_SEARCH",
		"--property=ProtectSystem=strict", "--property=ReadWritePaths=" + tlsFrontendRequests,
		"--property=ProtectHome=yes", "--property=PrivateTmp=yes", "--property=PrivateDevices=yes",
		"--property=ProtectKernelTunables=yes", "--property=ProtectKernelModules=yes", "--property=ProtectControlGroups=yes",
		"--property=RestrictAddressFamilies=AF_UNIX", "--property=RuntimeMaxSec=30s", "--property=TimeoutStopSec=5s",
		"--property=KillMode=control-group", "--", executable, "manage-tls", name}
}

// Recover private helper results left by a killed Agent. Requests from active
// helpers are at most 35 seconds old; only this fixed result namespace is read.
func cleanupManagedCertificateResponses() {
	if validateProtectedDirectoryChain(tlsFrontendRequests) != nil {
		return
	}
	entries, err := os.ReadDir(tlsFrontendRequests)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".result")
		if name == entry.Name() || !tlsRequestName.MatchString(name) {
			continue
		}
		path := filepath.Join(tlsFrontendRequests, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || validateOwner(info, "certificate response") != nil || time.Since(info.ModTime()) < 10*time.Minute {
			continue
		}
		_ = os.Remove(path)
		_ = os.Remove(filepath.Join(tlsFrontendRequests, name))
	}
}
