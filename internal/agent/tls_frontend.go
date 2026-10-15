package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const tlsFrontendRoot = "/etc/qagent/tls-frontends"
const tlsFrontendState = "/var/lib/qcontrolhub-tls"

var tlsFrontendMutex sync.Mutex

type tlsDeployment struct {
	Frontends []core.TLSFrontend `json:"frontends"`
	Stopped   bool               `json:"stopped,omitempty"`
}
type tlsDeployments map[string]tlsDeployment

type tlsFrontendManager struct {
	root     string
	state    string
	services *ServiceManager
	updater  *CoreUpdater
}

func (m *tlsFrontendManager) load() (tlsDeployments, error) {
	path := filepath.Join(m.root, "deployments.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return tlsDeployments{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4<<20 {
		return nil, errors.New("unsafe managed TLS manifest")
	}
	if err = validateOwner(info, "TLS manifest"); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return tlsDeployments{}, nil
	}
	if err != nil {
		return nil, err
	}
	var result tlsDeployments
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	for service, d := range result {
		if !safeServiceName(service) {
			return nil, errors.New("unsafe managed TLS service binding")
		}
		for _, f := range d.Frontends {
			if err = f.Validate(); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func frontendKey(f core.TLSFrontend) string {
	sum := sha256.Sum256([]byte(f.Domain + f.Certificate + f.PrivateKey))
	return hex.EncodeToString(sum[:12])
}

func activeTLSFrontends(deployments tlsDeployments) ([]core.TLSFrontend, error) {
	var result []core.TLSFrontend
	owners := map[int]string{}
	paths := map[string]bool{}
	backends := map[int]bool{}
	services := make([]string, 0, len(deployments))
	for service := range deployments {
		services = append(services, service)
	}
	sort.Strings(services)
	for _, service := range services {
		d := deployments[service]
		if d.Stopped {
			continue
		}
		for _, f := range d.Frontends {
			if err := f.Validate(); err != nil {
				return nil, err
			}
			if mode := owners[f.PublicPort]; mode != "" && mode != f.Mode {
				return nil, fmt.Errorf("公网端口 %d 已用于 %s，不能同时用于 %s", f.PublicPort, mode, f.Mode)
			}
			route := strconv.Itoa(f.PublicPort) + f.Path
			if paths[route] {
				return nil, fmt.Errorf("公网端口 %d 上的路径 %s 已被其他入站使用", f.PublicPort, f.Path)
			}
			owners[f.PublicPort] = f.Mode
			paths[route] = true
			backends[f.BackendPort] = true
			result = append(result, f)
		}
	}
	for port, mode := range owners {
		if backends[port] {
			return nil, fmt.Errorf("公网反代端口 %d 与内部端口冲突", port)
		}
		if port == 80 && mode == "caddy" {
			return nil, errors.New("Caddy 自动证书需要独立的 HTTP 验证端口 80，公网 HTTPS 端口不能为 80")
		}
	}
	for _, f := range result {
		if f.Mode == "caddy" && owners[80] != "" {
			return nil, errors.New("端口 80 已用于 TLS 反代，Caddy HTTP 证书验证无法使用")
		}
	}
	return result, nil
}

func renderCaddyFrontends(frontends []core.TLSFrontend, state string) (string, error) {
	servers := map[string]any{}
	domains := map[string]bool{}
	byPort := map[int][]any{}
	for _, f := range frontends {
		if f.Mode != "caddy" {
			continue
		}
		domains[f.Domain] = true
		byPort[f.PublicPort] = append(byPort[f.PublicPort], map[string]any{"match": []any{map[string]any{"path": []string{f.Path}}}, "handle": []any{map[string]any{"handler": "reverse_proxy", "headers": map[string]any{"request": map[string]any{"set": map[string]any{"X-Forwarded-For": []string{"{http.request.remote.host}"}}}}, "upstreams": []any{map[string]any{"dial": net.JoinHostPort("127.0.0.1", strconv.Itoa(f.BackendPort))}}, "transport": map[string]any{"protocol": "http", "versions": []string{"1.1"}}}}, "terminal": true})
	}
	names := []string{}
	for domain := range domains {
		names = append(names, domain)
	}
	sort.Strings(names)
	for port, routes := range byPort {
		servers["https_"+strconv.Itoa(port)] = map[string]any{"listen": []string{":" + strconv.Itoa(port)}, "routes": append(routes, map[string]any{"handle": []any{map[string]any{"handler": "static_response", "status_code": 404}}}), "protocols": []string{"h1"}, "tls_connection_policies": []any{map[string]any{"alpn": []string{"http/1.1"}}}, "automatic_https": map[string]any{"disable_redirects": true}}
	}
	config := map[string]any{"admin": map[string]any{"listen": "unix/" + filepath.Join(state, "caddy-admin.sock")}, "storage": map[string]any{"module": "file_system", "root": filepath.Join(state, "caddy")}, "apps": map[string]any{"http": map[string]any{"servers": servers}, "tls": map[string]any{"certificates": map[string]any{"automate": names}, "automation": map[string]any{"policies": []any{map[string]any{"subjects": names, "issuers": []any{map[string]any{"module": "acme", "challenges": map[string]any{"tls-alpn": map[string]any{"disabled": true}}}}}}}}}}
	value, err := json.MarshalIndent(config, "", "  ")
	return string(value), err
}

func renderNginxFrontends(frontends []core.TLSFrontend, root, state string) string {
	var body strings.Builder
	fmt.Fprintf(&body, "daemon off;\npid %s;\nerror_log stderr info;\nworker_processes 1;\nevents { worker_connections 1024; }\nhttp {\n access_log off;\n client_body_temp_path %s;\n proxy_temp_path %s;\n fastcgi_temp_path %s;\n uwsgi_temp_path %s;\n scgi_temp_path %s;\n map $http_upgrade $qch_connection { default upgrade; '' close; }\n", filepath.Join(state, "nginx.pid"), filepath.Join(state, "client_temp"), filepath.Join(state, "proxy_temp"), filepath.Join(state, "fastcgi_temp"), filepath.Join(state, "uwsgi_temp"), filepath.Join(state, "scgi_temp"))
	ports := map[int][]core.TLSFrontend{}
	for _, f := range frontends {
		if f.Mode == "nginx" {
			ports[f.PublicPort] = append(ports[f.PublicPort], f)
		}
	}
	sorted := []int{}
	for port := range ports {
		sorted = append(sorted, port)
	}
	sort.Ints(sorted)
	for _, port := range sorted {
		domains := map[string]core.TLSFrontend{}
		for _, f := range ports[port] {
			domains[f.Domain] = f
		}
		names := []string{}
		for name := range domains {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			f := domains[name]
			key := frontendKey(f)
			fmt.Fprintf(&body, " server {\n  listen %d ssl;\n  server_name %s;\n  ssl_protocols TLSv1.2 TLSv1.3;\n  ssl_certificate %s;\n  ssl_certificate_key %s;\n", port, name, filepath.Join(root, key+".crt"), filepath.Join(root, key+".key"))
			// All paths on a port are available in every virtual host. HTTP Host is
			// independent of TLS SNI, including clients connecting to an IP address.
			for _, route := range ports[port] {
				fmt.Fprintf(&body, "  location = %s {\n   proxy_pass http://127.0.0.1:%d;\n   proxy_http_version 1.1;\n   proxy_set_header Host $http_host;\n   proxy_set_header X-Forwarded-For $remote_addr;\n   proxy_set_header Upgrade $http_upgrade;\n   proxy_set_header Connection $qch_connection;\n   proxy_buffering off;\n   proxy_request_buffering off;\n   proxy_read_timeout 86400s;\n   proxy_send_timeout 86400s;\n  }\n", route.Path, route.BackendPort)
			}
			body.WriteString("  location / { return 404; }\n }\n")
		}
	}
	body.WriteString("}\n")
	return body.String()
}

func (m *tlsFrontendManager) prepareDirectories() error {
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return err
	}
	for _, path := range []string{m.root, m.state} {
		if err = os.MkdirAll(path, 0750); err != nil {
			return err
		}
		if path == m.root {
			if err = validateProtectedDirectoryChain(path); err != nil {
				return err
			}
		} else {
			if err = validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
				return err
			}
			info, statErr := os.Lstat(path)
			if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("unsafe TLS state root")
			}
		}
		if path == m.root {
			if err = os.Chmod(path, 0750); err != nil {
				return err
			}
		}
		if path == m.root {
			if err = os.Chown(path, 0, int(identity.gid)); err != nil {
				return err
			}
		}
	}
	root, err := os.OpenRoot(m.state)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range []string{"caddy", "client_temp", "proxy_temp", "fastcgi_temp", "uwsgi_temp", "scgi_temp"} {
		if err = root.Mkdir(name, 0750); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, statErr := root.Lstat(name)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe TLS state directory")
		}
		if err = root.Chown(name, int(identity.uid), int(identity.gid)); err != nil {
			return err
		}
	}

	// Nginx writes its pid in the state root; configuration remains root-owned.
	return os.Chown(m.state, int(identity.uid), int(identity.gid))
}
func (m *tlsFrontendManager) writeConfig(path, content string, private bool) error {
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return err
	}
	metadata := fileMetadata{mode: 0640, uid: 0, gid: int(identity.gid), ownershipKnown: true}
	if private {
		metadata.mode = 0600
	}
	backup, err := atomicDeployWithDefaultMetadata(path, content, metadata)
	if backup != "" {
		_ = os.Remove(backup)
	}
	return err
}

func (m *tlsFrontendManager) checkPorts(previous, next []core.TLSFrontend) error {
	owned := map[int]bool{}
	for _, f := range previous {
		owned[f.PublicPort] = true
		if f.Mode == "caddy" {
			owned[80] = true
		}
	}
	ports := map[int]bool{}
	for _, f := range next {
		ports[f.PublicPort] = true
		if f.Mode == "caddy" {
			ports[80] = true
		}
	}
	for port := range ports {
		if owned[port] {
			continue
		}
		listener, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(port)))
		if err != nil {
			return fmt.Errorf("反代端口 %d 不可用：%w；检查现有 Caddy/Nginx、其他服务或端口分配", port, err)
		}
		listener.Close()
	}
	return nil
}

func (m *tlsFrontendManager) prepare(ctx context.Context, frontends []core.TLSFrontend) error {
	if len(frontends) == 0 {
		return nil
	}
	if err := m.prepareDirectories(); err != nil {
		return err
	}
	modes := map[string]bool{}
	for _, f := range frontends {
		modes[f.Mode] = true
		if f.Mode == "nginx" {
			if _, err := tls.X509KeyPair([]byte(f.Certificate), []byte(f.PrivateKey)); err != nil {
				return fmt.Errorf("托管 Nginx 证书无效：%w", err)
			}
			if err := m.writeConfig(filepath.Join(m.root, frontendKey(f)+".crt"), f.Certificate, false); err != nil {
				return err
			}
			if err := m.writeConfig(filepath.Join(m.root, frontendKey(f)+".key"), f.PrivateKey, false); err != nil {
				return err
			}
		}
	}
	for mode := range modes {
		if err := m.ensureDependency(ctx, mode); err != nil {
			return err
		}
	}
	return nil
}

func (m *tlsFrontendManager) validateConfigs(ctx context.Context, frontends []core.TLSFrontend) error {
	modes := map[string]bool{}
	for _, f := range frontends {
		modes[f.Mode] = true
	}
	for mode := range modes {
		content := renderNginxFrontends(frontends, m.root, m.state)
		if mode == "caddy" {
			var err error
			content, err = renderCaddyFrontends(frontends, m.state)
			if err != nil {
				return err
			}
		}
		path := filepath.Join(m.root, mode+".staged.conf")
		if err := m.writeConfig(path, content, false); err != nil {
			return err
		}
		var output string
		var err error
		identity, identityErr := managedCoreServiceIdentity()
		if identityErr != nil {
			return identityErr
		}
		if mode == "caddy" {
			output, err = runInDirectoryWithEnvironmentAndIdentity(ctx, m.state, "", managedCaddyBinary, &identity, "validate", "--config", path)
		} else {
			output, err = runInDirectoryWithEnvironmentAndIdentity(ctx, m.state, "", managedNginxBinary(), &identity, "-t", "-p", m.state+"/", "-c", path)
		}
		_ = os.Remove(path)
		if err != nil {
			return fmt.Errorf("%s 反代配置校验失败：%w：%s", mode, err, output)
		}
	}
	return nil
}

func (m *tlsFrontendManager) stage(ctx context.Context, service string, frontends []core.TLSFrontend) error {
	tlsFrontendMutex.Lock()
	defer tlsFrontendMutex.Unlock()
	deployments, err := m.load()
	if err != nil {
		return err
	}
	previous, err := activeTLSFrontends(deployments)
	if err != nil {
		return err
	}
	deployments[service] = tlsDeployment{Frontends: frontends}
	next, err := activeTLSFrontends(deployments)
	if err != nil {
		return err
	}
	if len(next) == 0 {
		return nil
	}
	if err = m.checkPorts(previous, next); err != nil {
		return err
	}
	if err = m.prepare(ctx, next); err != nil {
		return err
	}
	return m.validateConfigs(ctx, next)
}

func (m *tlsFrontendManager) sync(ctx context.Context, deployments tlsDeployments) error {
	frontends, err := activeTLSFrontends(deployments)
	if err != nil {
		return err
	}
	modes := map[string]bool{}
	for _, f := range frontends {
		modes[f.Mode] = true
	}
	for _, mode := range []string{"caddy", "nginx"} {
		service := "qagent-tls-" + mode
		if m.services.Kind() == ServiceManagerSystemd {
			service += ".service"
		}
		if !modes[mode] {
			if _, statErr := os.Stat(filepath.Join(m.root, mode+".conf")); statErr == nil {
				if _, err = m.services.command(ctx, service, core.ActionStop); err != nil {
					return err
				}
				if m.services.Kind() == ServiceManagerSystemd {
					_, err = run(ctx, m.services.enableHelper(), "disable", service)
				} else {
					_, err = run(ctx, m.services.enableHelper(), "del", service, "default")
				}
				if err != nil {
					return err
				}
			}
			continue
		}
	}
	// Release ports owned by an outgoing provider before starting its replacement.
	for _, mode := range []string{"caddy", "nginx"} {
		if !modes[mode] {
			continue
		}
		service := "qagent-tls-" + mode
		if m.services.Kind() == ServiceManagerSystemd {
			service += ".service"
		}
		content := renderNginxFrontends(frontends, m.root, m.state)
		if mode == "caddy" {
			content, err = renderCaddyFrontends(frontends, m.state)
			if err != nil {
				return err
			}
		}
		path := filepath.Join(m.root, mode+".conf")
		stored, _ := os.ReadFile(path)
		if err = m.writeConfig(path, content, false); err != nil {
			return err
		}
		if err = m.ensureService(ctx, mode); err != nil {
			return err
		}
		status, _ := m.services.status(ctx, service)
		if status == "active" && string(stored) == content {
			continue
		}
		var output string
		var commandErr error
		if status == "active" {
			if mode == "caddy" {
				output, commandErr = run(ctx, managedCaddyBinary, "reload", "--config", path, "--address", "unix/"+filepath.Join(m.state, "caddy-admin.sock"))
			} else {
				output, commandErr = run(ctx, managedNginxBinary(), "-p", m.state+"/", "-c", path, "-s", "reload")
			}
		} else {
			output, commandErr = m.services.command(ctx, service, core.ActionStart)
		}
		if commandErr != nil {
			return fmt.Errorf("%s 反代启动失败：%w；%s；%s", mode, commandErr, output, serviceFailureJournal(ctx, service))
		}
		serviceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = waitForServiceState(serviceCtx, "active", time.Second, 100*time.Millisecond, func(ctx context.Context) (string, error) { return m.services.status(ctx, service) })
		cancel()
		if err != nil {
			return fmt.Errorf("%s 反代未进入运行状态：%w；%s", mode, err, serviceFailureJournal(ctx, service))
		}
	}
	return nil
}

func (m *tlsFrontendManager) apply(ctx context.Context, service string, frontends []core.TLSFrontend, action core.Action) error {
	tlsFrontendMutex.Lock()
	defer tlsFrontendMutex.Unlock()
	previous, err := m.load()
	if err != nil {
		return err
	}
	if len(previous) == 0 && len(frontends) == 0 {
		return nil
	}
	next := tlsDeployments{}
	for key, value := range previous {
		next[key] = value
	}
	switch action {
	case core.ActionDeploy:
		if len(frontends) == 0 {
			delete(next, service)
		} else {
			next[service] = tlsDeployment{Frontends: frontends}
		}
	case core.ActionStop:
		if d, ok := next[service]; ok {
			d.Stopped = true
			next[service] = d
		}
	case core.ActionStart, core.ActionRestart:
		if d, ok := next[service]; ok {
			d.Stopped = false
			next[service] = d
		}
	case core.ActionUninstall:
		delete(next, service)
	default:
		return errors.New("unsupported managed TLS lifecycle action")
	}
	oldActive, err := activeTLSFrontends(previous)
	if err != nil {
		return err
	}
	newActive, err := activeTLSFrontends(next)
	if err != nil {
		return err
	}
	if err = m.checkPorts(oldActive, newActive); err != nil {
		return err
	}
	if err = m.prepare(ctx, newActive); err != nil {
		return err
	}
	if err = m.validateConfigs(ctx, newActive); err != nil {
		return err
	}
	err = m.sync(ctx, next)
	if err == nil && action != core.ActionStop && action != core.ActionUninstall {
		for _, f := range next[service].Frontends {
			if err = probeFrontendTLS(ctx, f); err != nil {
				break
			}
		}
	}
	if err == nil {
		var encoded []byte
		encoded, err = json.Marshal(next)
		if err == nil {
			err = m.writeConfig(filepath.Join(m.root, "deployments.json"), string(encoded), true)
		}
	}
	if err != nil {
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rollbackErr := m.sync(recovery, previous)
		return fmt.Errorf("托管 TLS 部署失败：%w；反代恢复结果：%v", err, rollbackErr)
	}
	return nil
}

func probeFrontendTLS(ctx context.Context, f core.TLSFrontend) error {
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: f.Domain, NextProtos: []string{"http/1.1"}}
	if f.Mode == "nginx" {
		config.RootCAs = x509.NewCertPool()
		if !config.RootCAs.AppendCertsFromPEM([]byte(f.Certificate)) {
			return errors.New("Nginx 公共证书无效")
		}
	}
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastErr error
	for {
		dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 2 * time.Second}, Config: config}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(f.PublicPort)))
		if err == nil {
			conn.Close()
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("TLS 证书验证超时：%w；检查域名 DNS、节点入站端口 80 和公网 HTTPS 端口 %d，以及 Caddy 日志：%v", ctx.Err(), f.PublicPort, lastErr)
		case <-deadline.C:
			return fmt.Errorf("TLS 证书尚未就绪；检查 %s 的 DNS、端口 80、公网端口 %d 和 ACME 日志：%w", f.Domain, f.PublicPort, lastErr)
		case <-ticker.C:
		}
	}
}
