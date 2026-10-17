package agent

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"golang.org/x/crypto/acme"
)

// Optional real CA + existing Nginx service gate. The panel never listens on
// the challenge port: Pebble validates Nginx's route to the Agent handler.
func TestCertificateHTTP01ExistingNginxWithLocalACME(t *testing.T) {
	binary := os.Getenv("QCH_PEBBLE_BIN")
	nginx := os.Getenv("QCH_NGINX_BIN")
	if binary == "" || nginx == "" {
		t.Skip("requires local Pebble and Nginx binaries")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	service := &certificateChallengeService{routes: map[string]core.HTTPChallenge{}}
	backend := httptest.NewServer(service)
	defer backend.Close()
	defer service.Close()
	port := func() int {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	websitePort, caPort, managementPort := port(), port(), port()
	nginxConfig := "daemon off; master_process off; error_log " + filepath.Join(root, "nginx-error.log") + "; pid " + filepath.Join(root, "nginx.pid") + "; events {} http { access_log off; client_body_temp_path " + root + "; proxy_temp_path " + root + "; fastcgi_temp_path " + root + "; uwsgi_temp_path " + root + "; scgi_temp_path " + root + "; server { listen 127.0.0.1:" + strconv.Itoa(websitePort) + "; listen [::1]:" + strconv.Itoa(websitePort) + "; location /.well-known/acme-challenge/ { proxy_pass " + backend.URL + "; proxy_set_header Host $host; } location / { return 200 'existing business'; } } }"
	configPath := filepath.Join(root, "nginx.conf")
	if err := os.WriteFile(configPath, []byte(nginxConfig), 0600); err != nil {
		t.Fatal(err)
	}
	start := func(path string, args ...string) {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0")
		log, err := os.CreateTemp(root, "process-*.log")
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout = log
		cmd.Stderr = log
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); log.Close() })
	}
	start(nginx, "-p", root, "-c", configPath)
	module := os.Getenv("QCH_PEBBLE_CERT_DIR")
	config := map[string]any{"pebble": map[string]any{"listenAddress": "127.0.0.1:" + strconv.Itoa(caPort), "managementListenAddress": "127.0.0.1:" + strconv.Itoa(managementPort), "certificate": filepath.Join(module, "localhost/cert.pem"), "privateKey": filepath.Join(module, "localhost/key.pem"), "httpPort": websitePort, "tlsPort": port(), "retryAfter": map[string]int{"authz": 1, "order": 1}, "keyAlgorithm": "ecdsa"}}
	raw, _ := json.Marshal(config)
	path := filepath.Join(root, "pebble.json")
	_ = os.WriteFile(path, raw, 0600)
	start(binary, "-config", path, "-dnsserver", os.Getenv("QCH_PEBBLE_DNS"))
	directory := "https://localhost:" + strconv.Itoa(caPort) + "/dir"
	for {
		req, _ := http.NewRequestWithContext(ctx, "GET", directory, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("CA startup: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	old := certificates.Directories["letsencrypt-staging"]
	certificates.Directories["letsencrypt-staging"] = directory
	defer func() { certificates.Directories["letsencrypt-staging"] = old }()
	_, key, err := certificates.NewKey("ecdsa256")
	if err != nil {
		t.Fatal(err)
	}
	account, err := certificates.Register(ctx, "letsencrypt-staging", "node@example.test", certificates.ACMEAccount{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	client, err := certificates.Client("letsencrypt-staging", account)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"http-node.example.test", "http-alt.example.test"}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(names...))
	if err != nil {
		t.Fatal(err)
	}
	orderURL := order.URI
	for _, url := range order.AuthzURLs {
		auth, err := client.GetAuthorization(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		for _, challenge := range auth.Challenges {
			if challenge.Type != "http-01" {
				continue
			}
			value, err := client.HTTP01ChallengeResponse(challenge.Token)
			if err != nil {
				t.Fatal(err)
			}
			service.mu.Lock()
			service.routes[auth.Identifier.Value+"/"+challenge.Token] = core.HTTPChallenge{OrderID: orderURL, Domain: auth.Identifier.Value, Token: challenge.Token, Value: value, ExpiresAt: time.Now().Add(time.Minute)}
			service.mu.Unlock()
			if _, err = client.Accept(ctx, challenge); err != nil {
				t.Fatal(err)
			}
		}
	}
	ready, err := client.WaitOrder(ctx, orderURL)
	if err != nil {
		t.Fatal(err)
	}
	ready.URI = orderURL
	csr, private, err := certificates.CSR(names, "ecdsa256")
	if err != nil {
		t.Fatal(err)
	}
	pair, err := certificates.Finalize(ctx, client, ready, csr, private)
	if err != nil {
		t.Fatal(err)
	}
	if err = certificates.Covers(pair, names); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get("http://127.0.0.1:" + strconv.Itoa(websitePort) + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "existing business" {
		t.Fatal("existing business route changed")
	}
	service.Close()
	response, err = http.Get("http://127.0.0.1:" + strconv.Itoa(websitePort) + "/.well-known/acme-challenge/" + strings.Repeat("a", 24))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("challenge cleanup failed")
	}
	t.Log("Pebble actually validated two HTTP-01 authorizations through running Nginx -> Agent handler; business route preserved; panel has no challenge listener")
}
