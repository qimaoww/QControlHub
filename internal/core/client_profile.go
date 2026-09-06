package core

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

const ClientProfileNameLabelPrefix = "client_profile_name_"

// ClientProfileNameLabel scopes client-only names to an engine and listening
// endpoint within one Agent. Tags and SS Rust array positions may change; they
// must not move a name to another port. No credentials enter this key.
func ClientProfileNameLabel(engine Engine, listen string, port int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", engine, strings.TrimSpace(listen), port)))
	return fmt.Sprintf("%s%x", ClientProfileNameLabelPrefix, digest)
}
