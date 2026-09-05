// Package qcontrolhub supplies the immutable installation resources bundled
// with QAgent. A binary-only upgrade must also upgrade the scripts and service
// templates used by subsequent explicit core install/import tasks.
package qcontrolhub

import (
	"embed"
	"io/fs"
)

// Keep the allowlist explicit: never embed enrollment files, credentials,
// operator configuration, tests, or the Agent's own service definition.
//
//go:embed deploy/bootstrap-core-services.sh deploy/existing-core-mapping.sh
//go:embed deploy/systemd/qagent-core-journal.conf
//go:embed deploy/systemd/qagent-mihomo.service deploy/systemd/qagent-xray.service
//go:embed deploy/systemd/qagent-sing-box.service deploy/systemd/qagent-shadowsocks-rust.service
//go:embed deploy/openrc/qagent-mihomo deploy/openrc/qagent-xray
//go:embed deploy/openrc/qagent-sing-box deploy/openrc/qagent-shadowsocks-rust
//go:embed examples/configs/mihomo-minimal.yaml examples/configs/xray-minimal.json
//go:embed examples/configs/sing-box-minimal.json examples/configs/shadowsocks-rust-minimal.json
var coreInstallAssets embed.FS

func CoreInstallAssets() fs.FS {
	return coreInstallAssets
}
