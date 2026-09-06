package core

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const ClientProfileNameLabelPrefix = "client_profile_name_"

// SS Rust IDs are descriptive metadata, not firewall identifiers. Share this
// validation between preset generation and Agent policy processing; firewall
// rules continue to use validated numeric ports, never interpolated names.
func ValidSSRustTag(tag string) bool {
	return tag != "" && strings.TrimSpace(tag) == tag && utf8.ValidString(tag) && utf8.RuneCountInString(tag) <= 64 && !strings.ContainsFunc(tag, unicode.IsControl)
}

// ClientProfileNameLabel scopes client-only names to an engine and listening
// endpoint within one Agent. Tags and SS Rust array positions may change; they
// must not move a name to another port. No credentials enter this key.
func ClientProfileNameLabel(engine Engine, listen string, port int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", engine, strings.TrimSpace(listen), port)))
	return fmt.Sprintf("%s%x", ClientProfileNameLabelPrefix, digest)
}
