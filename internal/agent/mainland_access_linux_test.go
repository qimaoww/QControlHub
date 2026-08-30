package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestParseMainlandIPv4Ranges(t *testing.T) {
	// Use a deterministic valid feed instead of relying on the network in unit
	// tests; the parser's lower/upper bounds are what the Agent enforces.
	var feed strings.Builder
	for index := 0; index < 1000; index++ {
		fmt.Fprintf(&feed, "10.%d.%d.0/24\n", index/256, index%256)
	}
	// Add duplicates to verify de-duplication while retaining a valid count.
	feed.WriteString(strings.Repeat("10.0.0.0/24\n", 3))
	ranges, err := parseMainlandIPv4Ranges(strings.NewReader(feed.String()))
	if err != nil || len(ranges) != 1000 {
		t.Fatalf("parse duplicate mainland ranges = %d, %v", len(ranges), err)
	}
	if _, err := parseMainlandIPv4Ranges(strings.NewReader("2001:db8::/32\n")); err == nil {
		t.Fatal("IPv6 mainland range was accepted by the IPv4 firewall parser")
	}
}

func TestLiveMainlandAccessManagerAppliesAndRestoresNFTables(t *testing.T) {
	if os.Getenv("QCH_LIVE_NFT_TEST") != "1" {
		t.Skip("QCH_LIVE_NFT_TEST is not enabled")
	}
	nftPath, err := exec.LookPath("nft")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manager := &MainlandAccessManager{
		backend:   &nftBackend{nftPath: nftPath, direct: true},
		cachePath: filepath.Join(root, mainlandRouteCacheName), statePath: filepath.Join(root, mainlandStateName),
	}
	manager.routesLoader = func(context.Context) ([]string, error) {
		result := make([]string, 0, 1000)
		for index := 0; index < 1000; index++ {
			result = append(result, fmt.Sprintf("10.%d.%d.0/24", index/256, index%256))
		}
		return result, nil
	}
	manager.aclWriter = func(string) error { return nil }
	policy := core.MainlandAccessPolicy{AgentID: "agt_live", ConfigVersion: 1, Engine: core.EngineShadowsocksRust, Tag: "ss-rust", Port: 48388, BlockMainlandSource: true}
	if err := manager.Deploy(context.Background(), []core.MainlandAccessPolicy{policy}, "agt_live"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Deploy(context.Background(), nil, "agt_live") })
	output, err := exec.Command(nftPath, "list", "table", "inet", mainlandFirewallTable).CombinedOutput()
	if err != nil || !strings.Contains(string(output), "tcp dport 48388") || !strings.Contains(string(output), "udp dport 48388") {
		t.Fatalf("live nftables table = %v\n%s", err, output)
	}
	if err := manager.Deploy(context.Background(), nil, "agt_live"); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(nftPath, "list", "table", "inet", mainlandFirewallTable).CombinedOutput(); err == nil {
		t.Fatalf("live nftables table remained after disable:\n%s", output)
	}
}

func TestParseMainlandDomains(t *testing.T) {
	var feed strings.Builder
	for index := 0; index < 1000; index++ {
		fmt.Fprintf(&feed, "+.example-%d.cn\n", index)
	}
	feed.WriteString("www.example.cn\n+.example-0.cn\n")
	domains, err := parseMainlandDomains(strings.NewReader(feed.String()))
	if err != nil || len(domains) != 1001 {
		t.Fatalf("parse mainland domains = %d, %v", len(domains), err)
	}
	if domains[0] != "||example-0.cn" || domains[1000] != "|www.example.cn" {
		t.Fatalf("unexpected ACL domain conversion: %q ... %q", domains[0], domains[1000])
	}
	if _, err := parseMainlandDomains(strings.NewReader("regexp:.*\\.cn\n")); err == nil {
		t.Fatal("unsupported mainland domain rule was accepted")
	}
}

func TestMainlandAccessManagerDeploysAndRemovesShadowsocksRustPortRules(t *testing.T) {
	root := t.TempDir()
	logPath, markerPath := filepath.Join(root, "nft.log"), filepath.Join(root, "table.exists")
	nftPath := filepath.Join(root, "nft")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case " $* " in
  *" list table "*) [ -f %q ] ;;
esac
contents=$(cat)
printf '%%s\n---\n' "$contents" >> %q
case "$contents" in
  *"add table"*) : > %q ;;
  *"delete table"*) rm -f %q ;;
esac
`, markerPath, logPath, markerPath, markerPath)
	if err := os.WriteFile(nftPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "agent-state.json")
	manager := &MainlandAccessManager{
		backend:   &nftBackend{nftPath: nftPath, direct: true},
		cachePath: filepath.Join(root, mainlandRouteCacheName), statePath: filepath.Join(root, mainlandStateName),
	}
	aclPath := filepath.Join(root, "block-cn.acl")
	manager.aclWriter = func(content string) error {
		return os.WriteFile(aclPath, []byte(content), 0o600)
	}
	manager.routesLoader = func(context.Context) ([]string, error) {
		result := make([]string, 0, 1000)
		for index := 0; index < 1000; index++ {
			result = append(result, fmt.Sprintf("10.%d.%d.0/24", index/256, index%256))
		}
		return result, nil
	}
	policy := core.MainlandAccessPolicy{AgentID: "agt_test", ConfigVersion: 1, Engine: core.EngineShadowsocksRust, Tag: "ss-rust", Port: 8388, BlockMainlandSource: true}
	if err := manager.Deploy(context.Background(), []core.MainlandAccessPolicy{policy}, "agt_test"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(contents)
	for _, expected := range []string{"cn_ipv4", "tcp dport 8388 reject", "udp dport 8388 reject"} {
		if !strings.Contains(log, expected) {
			t.Fatalf("nftables rules missing %q:\n%s", expected, log)
		}
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("unexpected Agent credential state side effect: %v", err)
	}
	if err := manager.Deploy(context.Background(), nil, "agt_test"); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(contents), "delete table inet "+mainlandFirewallTable) {
		t.Fatalf("disabled policy did not remove table: %v\n%s", err, contents)
	}
}

func TestMainlandAccessManagerWritesShadowsocksRustDestinationACL(t *testing.T) {
	root := t.TempDir()
	nftPath := filepath.Join(root, "nft")
	if err := os.WriteFile(nftPath, []byte("#!/bin/sh\ncat >/dev/null\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	aclPath := filepath.Join(root, "block-cn.acl")
	manager := &MainlandAccessManager{
		backend:   &nftBackend{nftPath: nftPath, direct: true},
		cachePath: filepath.Join(root, mainlandRouteCacheName), statePath: filepath.Join(root, mainlandStateName),
	}
	manager.routesLoader = func(context.Context) ([]string, error) { return []string{"1.0.1.0/24"}, nil }
	manager.domainsLoader = func(context.Context) ([]string, error) { return []string{"||example.cn", "|www.example.cn"}, nil }
	manager.aclWriter = func(content string) error { return os.WriteFile(aclPath, []byte(content), 0o600) }
	policy := core.MainlandAccessPolicy{
		AgentID: "agt_test", ConfigVersion: 1, Engine: core.EngineShadowsocksRust, Tag: "ss-rust", Port: 8388,
		BlockMainlandDestination: true,
	}
	if err := manager.Deploy(context.Background(), []core.MainlandAccessPolicy{policy}, "agt_test"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(aclPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"[outbound_block_list]", "1.0.1.0/24", "||example.cn", "|www.example.cn"} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("ACL missing %q:\n%s", expected, contents)
		}
	}
	if err := manager.Deploy(context.Background(), nil, "agt_test"); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(aclPath)
	if err != nil || strings.Contains(string(contents), "1.0.1.0/24") || !strings.Contains(string(contents), "[outbound_block_list]") {
		t.Fatalf("disabled policy did not restore an empty ACL: %v\n%s", err, contents)
	}
}
