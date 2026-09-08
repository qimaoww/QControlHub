package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestAccountingSavedInputIsRepeatable(t *testing.T) {
	plan, err := serverconfig.PrepareAccounting(core.EngineXray, `{"inbounds":[{"tag":"a","port":1080,"protocol":"socks"}],"outbounds":[{"tag":"direct","protocol":"freedom","settings":{"domainStrategy":"AsIs"}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(plan.Content), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := strings.Replace(plan.Content, `"domainStrategy": "AsIs"`, `"domainStrategy": "UseIP"`, 1)
	source, err := accountingUpdateInput(core.EngineXray, path, saved)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := serverconfig.PrepareAccounting(core.EngineXray, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := rememberAccountingInput(path, saved, prepared.Content); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(prepared.Content), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		source, err := accountingUpdateInput(core.EngineXray, path, saved)
		if err != nil {
			t.Fatal(err)
		}
		got, err := serverconfig.PrepareAccounting(core.EngineXray, source)
		if err != nil || got.Content != prepared.Content {
			t.Fatalf("repeat changed compiled output: %v", err)
		}
	}
	if info, err := os.Stat(accountingInputPath(path, saved)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("unsafe cached source: %v %v", info, err)
	}
	// A modified input never borrows another revision's trusted artifact map.
	modified := strings.Replace(saved, `"domainStrategy": "AsIs"`, `"domainStrategy": "UseIPv4"`, 1)
	if _, err := accountingUpdateInput(core.EngineXray, path, modified); err == nil {
		t.Fatal("accepted edited generated artifact")
	}
}
