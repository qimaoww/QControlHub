//go:build linux

package agent

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDecodeJournalCoreLogMapsManagedUnitsAndPriorities(t *testing.T) {
	t.Parallel()
	value := []byte(`{"MESSAGE":"accepted connection","_SYSTEMD_UNIT":"qagent-sing-box.service","PRIORITY":"4","__REALTIME_TIMESTAMP":"1787310000123456"}`)
	units := map[string]core.Engine{"qagent-sing-box.service": core.EngineSingBox}
	entry, _, ok := decodeJournalCoreLog(value, units)
	if !ok {
		t.Fatal("managed journal entry was rejected")
	}
	if entry.Engine != core.EngineSingBox || entry.Level != "warning" || entry.Message != "accepted connection" {
		t.Fatalf("decoded entry = %+v", entry)
	}
	if entry.LoggedAt.UnixMicro() != 1787310000123456 {
		t.Fatalf("logged_at = %s", entry.LoggedAt)
	}
	if _, _, ok := decodeJournalCoreLog([]byte(`{"MESSAGE":"ignored","_SYSTEMD_UNIT":"ssh.service","PRIORITY":"6"}`), units); ok {
		t.Fatal("unmanaged service journal was accepted")
	}
}

func TestDecodeJournalCoreLogAcceptsJournaldByteArrayMessage(t *testing.T) {
	t.Parallel()
	value := []byte(`{"MESSAGE":[27,91,51,50,109,73,78,70,79,27,91,48,109,32,105,110,98,111,117,110,100,47,109,105,120,101,100,91,102,105,120,116,117,114,101,93,32,115,116,97,114,116,101,100],"_SYSTEMD_UNIT":"qagent-sing-box.service","PRIORITY":"6","__REALTIME_TIMESTAMP":"1787310000123456","__CURSOR":"cursor-array"}`)
	entry, cursor, ok := decodeJournalCoreLog(value, map[string]core.Engine{
		"qagent-sing-box.service": core.EngineSingBox,
	})
	if !ok || cursor != "cursor-array" || entry.Engine != core.EngineSingBox ||
		entry.Message != "INFO inbound/mixed[fixture] started" {
		t.Fatalf("decoded journald byte-array entry = %+v, cursor=%q, ok=%v", entry, cursor, ok)
	}
	collector := NewCoreLogCollector(map[core.Engine]EngineSpec{})
	collector.appendJournal(entry, cursor)
	batch := collector.NextBatch()
	if batch == nil || len(batch.Entries) != 1 || batch.Entries[0].Message != entry.Message {
		t.Fatalf("journald byte-array batch = %+v", batch)
	}
	for name, message := range map[string]string{
		"fractional": `[27,1.5,65]`,
		"negative":   `[-1,65]`,
		"overflow":   `[256,65]`,
		"non-number": `[27,"65"]`,
	} {
		t.Run(name, func(t *testing.T) {
			invalid := []byte(`{"MESSAGE":` + message + `,"_SYSTEMD_UNIT":"qagent-sing-box.service"}`)
			if _, _, ok := decodeJournalCoreLog(invalid, map[string]core.Engine{"qagent-sing-box.service": core.EngineSingBox}); ok {
				t.Fatalf("invalid journald byte array was accepted: %s", invalid)
			}
		})
	}
	if _, ok := journalMessageField(make([]any, coreLogFileMaxLine+1)); ok {
		t.Fatal("oversized journald byte array was accepted")
	}
}

func TestSanitizeCoreLogMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ansi color", "\x1b[32mINFO\x1b[0m inbound started", "INFO inbound started"},
		{"ansi multi attrs", "\x1b[1;31mERROR\x1b[0m handler failed", "ERROR handler failed"},
		{"ansi csi with params", "\x1b[38;5;196mWARN\x1b[0m dial", "WARN dial"},
		{"lone esc", "up\x1bstream", "upstream"},
		{"nul", "before\x00after", "before�after"},
		{"invalid utf8", "bad\xffbyte", "bad�byte"},
		{"surrounding whitespace", "  message  ", "message"},
		{"plain unchanged", "plain text", "plain text"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sanitizeCoreLogMessage(test.in); got != test.want {
				t.Fatalf("sanitizeCoreLogMessage(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
	if got := sanitizeCoreLogMessage(""); got != "" {
		t.Fatalf("sanitizeCoreLogMessage(empty) = %q", got)
	}
	if got := sanitizeCoreLogMessage("\x1b[32m"); got != "" {
		t.Fatalf("sanitizeCoreLogMessage(only ansi) = %q", got)
	}
	long := strings.Repeat("a", core.MaxCoreLogMessageBytes) + "日志"
	if got := sanitizeCoreLogMessage(long); len([]byte(got)) > core.MaxCoreLogMessageBytes {
		t.Fatalf("sanitizeCoreLogMessage bound = %d, want <= %d", len([]byte(got)), core.MaxCoreLogMessageBytes)
	}
}

func TestDecodeJournalCoreLogBoundsMessages(t *testing.T) {
	t.Parallel()
	message := strings.Repeat("a", core.MaxCoreLogMessageBytes-1) + "日志"
	value := []byte(`{"MESSAGE":"` + message + `","_SYSTEMD_UNIT":"qagent-xray.service","PRIORITY":"6"}`)
	units := map[string]core.Engine{"qagent-xray.service": core.EngineXray}
	entry, _, ok := decodeJournalCoreLog(value, units)
	if !ok || len([]byte(entry.Message)) > core.MaxCoreLogMessageBytes {
		t.Fatalf("bounded entry = %d bytes, ok=%v", len([]byte(entry.Message)), ok)
	}
	nulEntry, _, ok := decodeJournalCoreLog([]byte(`{"MESSAGE":"before\u0000after","_SYSTEMD_UNIT":"qagent-xray.service","PRIORITY":"6"}`), units)
	if !ok || strings.ContainsRune(nulEntry.Message, '\x00') {
		t.Fatalf("NUL-containing journal entry was not sanitized: %+v, ok=%v", nulEntry, ok)
	}
}
