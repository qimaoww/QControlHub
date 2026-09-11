package core

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ClientProfileNameLabelPrefix = "client_profile_name_"
	// ClientProfileAddressLabelPrefix scopes an operator-provided client address
	// to one engine and listening endpoint, so every shared node keeps its own
	// address instead of rewriting the address of the whole Agent.
	ClientProfileAddressLabelPrefix = "client_profile_address_"
	// ClientProfileFamilyLabelPrefix scopes the selected address family the same
	// way, so one shared node can pin IPv4 while another stays automatic. The
	// prefix deliberately does not nest inside ClientProfileAddressLabelPrefix.
	ClientProfileFamilyLabelPrefix = "client_profile_family_"
)

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
	return ClientProfileNameLabelPrefix + clientProfileDigest(engine, listen, port)
}

// ClientProfileAddressLabel scopes an operator-provided client connection
// address to one engine and listening endpoint within one Agent.
func ClientProfileAddressLabel(engine Engine, listen string, port int) string {
	return ClientProfileAddressLabelPrefix + clientProfileDigest(engine, listen, port)
}

// ClientProfileFamilyLabel scopes the selected client address family to one
// engine and listening endpoint within one Agent.
func ClientProfileFamilyLabel(engine Engine, listen string, port int) string {
	return ClientProfileFamilyLabelPrefix + clientProfileDigest(engine, listen, port)
}

func clientProfileDigest(engine Engine, listen string, port int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", engine, strings.TrimSpace(listen), port)))
	return fmt.Sprintf("%x", digest)
}
