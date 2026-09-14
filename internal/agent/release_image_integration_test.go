package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/release"
)

// deploy/tests/release-image.sh supplies disposable containers, not a user's
// deployment. Exercise Nginx, enrollment, WSS policy, the authenticated manifest
// endpoint, and the real Agent downloader without installing a host service.
func TestReleaseImageEndToEnd(t *testing.T) {
	origin := os.Getenv("QCH_RELEASE_TEST_URL")
	if origin == "" {
		t.Skip("run make release-image-test to exercise the distribution images")
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Hostname() != "127.0.0.1" || parsed.Scheme != "http" {
		t.Fatal("release image test requires a disposable loopback HTTP deployment")
	}
	keyPath := os.Getenv("QCH_RELEASE_TEST_KEY")
	expectedVersion := os.Getenv("QCH_RELEASE_TEST_VERSION")
	if keyPath == "" || expectedVersion == "" || os.Getenv("QCH_RELEASE_TEST_ADMIN_TOKEN") == "" {
		t.Fatal("release image test fixture is incomplete")
	}
	publicKey, err := release.LoadPublicKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	httpClient := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("release test redirects are forbidden")
		},
	}
	request := func(method, path string, body any, headers http.Header, want int) []byte {
		t.Helper()
		var content []byte
		if body != nil {
			content, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		req.Header = headers.Clone()
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s %s: got %s, want %d", method, path, response.Status, want)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, int64(core.MaxAgentBinaryBytes)+1))
		if err != nil || len(data) > core.MaxAgentBinaryBytes {
			t.Fatalf("read %s: size=%d error=%v", path, len(data), err)
		}
		return data
	}
	var token core.EnrollmentTokenCreated
	if err := json.Unmarshal(request("POST", "/api/v1/enrollment-tokens", map[string]string{
		"name": "signed-release-integration",
	}, http.Header{"Authorization": {"Bearer " + os.Getenv("QCH_RELEASE_TEST_ADMIN_TOKEN")}}, http.StatusCreated), &token); err != nil {
		t.Fatal(err)
	}
	enrollmentHeaders := http.Header{"X-Qcontrolhub-Enrollment": {token.Token}}
	identityPublic, identityPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var enrolled core.EnrollResponse
	if err := json.Unmarshal(request("POST", "/agent/v1/enroll", core.EnrollRequest{
		Name: token.Name, OS: "linux", Arch: runtime.GOARCH,
		Capabilities: []core.Engine{core.EngineMihomo},
		Features:     []string{core.AgentFeatureSelfUpgrade}, PublicKey: authn.EncodePublicKey(identityPublic),
	}, http.Header{"Authorization": {"Bearer " + token.Token}}, http.StatusCreated), &enrolled); err != nil {
		t.Fatal(err)
	}
	client := &Client{
		config: ClientConfig{ServerURL: origin, ReleasePublicKey: keyPath},
		creds:  credentials{AgentID: enrolled.AgentID, PrivateKey: authn.EncodePrivateKey(identityPrivate)},
		http:   httpClient,
	}
	websocketURL := "ws" + strings.TrimPrefix(origin, "http") + "/agent/v1/connect"
	handshake, err := http.NewRequestWithContext(ctx, "GET", websocketURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authn.SignRequest(handshake, nil, enrolled.AgentID, identityPrivate, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	connection, _, err := websocket.Dial(ctx, websocketURL, &websocket.DialOptions{
		HTTPClient: httpClient, HTTPHeader: handshake.Header, Subprotocols: []string{"qcontrolhub.agent.v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	var hello core.WireMessage
	if err := wsjson.Read(ctx, connection, &hello); err != nil {
		t.Fatal(err)
	}
	if hello.Type != core.WireHello {
		t.Fatalf("unexpected initial WSS message: %s", hello.Type)
	}
	// Policy is capability-gated on this session's first full heartbeat, not
	// on features from enrollment or a previous connection.
	if err := wsjson.Write(ctx, connection, core.WireMessage{
		Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{
			Version: "previous-release", OS: "linux", Arch: runtime.GOARCH,
			Features: []string{core.AgentFeatureSelfUpgrade, core.AgentFeatureManagedPolicy},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var policy core.WireMessage
	if err := wsjson.Read(ctx, connection, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Type != core.WireAgentPolicy || policy.AgentPolicy == nil || policy.AgentPolicy.ControlPlaneVersion != expectedVersion {
		t.Fatalf("WSS did not report the packaged control-plane version: %+v", policy.AgentPolicy)
	}
	client.controlPlaneVersion = policy.AgentPolicy.ControlPlaneVersion
	request("GET", releaseManifestPath, nil, nil, http.StatusUnauthorized)
	manifest, err := client.fetchReleaseManifest(ctx, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Verify(publicKey); err != nil {
		t.Fatal(err)
	}
	list := request("GET", "/install-assets/SHA256SUMS", nil, nil, http.StatusOK)
	signature, err := release.DecodeChecksumsSignature(request("GET", "/install-assets/SHA256SUMS.sig", nil, nil, http.StatusOK))
	if err != nil {
		t.Fatal(err)
	}
	if err := release.VerifyChecksums(list, signature, publicKey); err != nil {
		t.Fatal(err)
	}
	if string(list) != release.ChecksumList(manifest.Artifacts) {
		t.Fatal("web checksum list and control-plane manifest describe different releases")
	}
	for _, artifact := range manifest.Artifacts {
		data := request("GET", artifact.Path, nil, enrollmentHeaders, http.StatusOK)
		if release.Digest(data) != artifact.SHA256 || int64(len(data)) != artifact.Size {
			t.Fatalf("packaged artifact does not match the signed manifest: %s", artifact.Path)
		}
	}
	_, version, _, err := client.downloadAgentBinaryOnce(ctx, httpClient, t.TempDir())
	if err != nil || version != expectedVersion {
		t.Fatalf("real Agent upgrade download failed: version=%q error=%v", version, err)
	}
	client.controlPlaneVersion = "not-the-signed-version"
	failedDirectory := t.TempDir()
	if _, _, _, err := client.downloadAgentBinaryOnce(ctx, httpClient, failedDirectory); err == nil || !strings.Contains(err.Error(), "does not match control plane version") {
		t.Fatalf("mismatched panel version was not rejected: %v", err)
	}
	if files, err := os.ReadDir(failedDirectory); err != nil || len(files) != 0 {
		t.Fatalf("failed upgrade left a candidate behind: files=%v error=%v", files, err)
	}
	t.Logf("verified enrollment, WSS policy, %d shipped artifacts, and the signed Agent upgrade download", len(manifest.Artifacts))
}
