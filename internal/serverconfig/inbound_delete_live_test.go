package serverconfig

import (
	"os"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestInboundDeleteObservedConfiguration(t *testing.T) {
	path := os.Getenv("QCH_DELETE_CONFIG")
	if path == "" {
		t.Skip("private observed configuration")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = PresetAccountingSource(core.EngineSingBox, string(b))
	if e != nil {
		t.Fatal(e)
	}
}
