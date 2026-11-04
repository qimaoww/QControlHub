package serverconfig

import (
	"encoding/json"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

func encodeAccountingDocument(engine core.Engine, root map[string]any) []byte {
	var data []byte
	if engine == core.EngineMihomo {
		data, _ = yaml.Marshal(root)
	} else {
		data, _ = json.Marshal(root)
	}
	return data
}
