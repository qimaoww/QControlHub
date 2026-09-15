//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSystemdConsoleReadinessCapturesLogsAfterTailPosition(t *testing.T) {
	for _, test := range []struct {
		name, cursor string
	}{
		{name: "existing journal cursor", cursor: "fixture-cursor"},
		{name: "empty journal bounded timestamp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			feed := filepath.Join(t.TempDir(), "journal-feed")
			if err := os.WriteFile(feed, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			helper := filepath.Join(t.TempDir(), "journalctl-fixture")
			script := `#!/bin/sh
set -eu
show_cursor=0
after_cursor=0
for argument in "$@"; do
	case "$argument" in
		--show-cursor) show_cursor=1 ;;
		--after-cursor=fixture-cursor|--since=@*) after_cursor=1 ;;
	esac
done
if [ "$show_cursor" = 1 ]; then
	if [ -n "$QCH_TEST_JOURNAL_CURSOR" ]; then
		printf '%s\n' "-- cursor: $QCH_TEST_JOURNAL_CURSOR"
	fi
	exit 0
fi
sleep 1
if [ "$after_cursor" = 1 ]; then
	exec tail -n +1 -f "$QCH_TEST_JOURNAL_FEED"
fi
exec tail -n 0 -f "$QCH_TEST_JOURNAL_FEED"
`
			if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			previousJournalctl := journalctlPath
			journalctlPath = helper
			t.Cleanup(func() { journalctlPath = previousJournalctl })
			t.Setenv("QCH_TEST_JOURNAL_FEED", feed)
			t.Setenv("QCH_TEST_JOURNAL_CURSOR", test.cursor)

			collector := NewCoreLogCollector(map[core.Engine]EngineSpec{
				core.EngineSingBox: {Service: "qagent-sing-box.service"},
			})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { collector.Run(ctx); close(done) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("systemd reader-ready fixture leaked")
				}
			}()

			content := `{"log":{"level":"info","timestamp":true}}`
			if err := collector.PrepareImportedSingBoxSource(context.Background(), nil, content); err != nil {
				t.Fatal(err)
			}
			line := `{"MESSAGE":"startup after ready","_SYSTEMD_UNIT":"qagent-sing-box.service","PRIORITY":"6","__CURSOR":"fixture-entry-cursor"}` + "\n"
			file, err := os.OpenFile(feed, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString(line); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if entry, ok := waitForLine(t, collector, "startup after ready"); !ok || entry.Engine != core.EngineSingBox {
				t.Fatalf("systemd startup entry after reader readiness = %+v, ok=%v", entry, ok)
			}
		})
	}
}

func TestSystemdJournalReaderReconnectsAfterLastCursor(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "journalctl-reconnect-fixture")
	script := `#!/bin/sh
set -eu
mode=unknown
for argument in "$@"; do
	case "$argument" in
		--show-cursor) mode=capture ;;
		--after-cursor=fixture-cursor-0) mode=first ;;
		--after-cursor=fixture-cursor-1) mode=second ;;
	esac
done
case "$mode" in
	capture)
		printf '%s\n' '-- cursor: fixture-cursor-0'
		;;
	first)
		printf '%s\n' '{"MESSAGE":"before journal reconnect","_SYSTEMD_UNIT":"qagent-sing-box.service","PRIORITY":"6","__CURSOR":"fixture-cursor-1"}'
		;;
	second)
		printf '%s\n' '{"MESSAGE":"after journal reconnect","_SYSTEMD_UNIT":"qagent-sing-box.service","PRIORITY":"6","__CURSOR":"fixture-cursor-2"}'
		while :; do sleep 1; done
		;;
	*) exit 2 ;;
esac
`
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	previousJournalctl := journalctlPath
	journalctlPath = helper
	t.Cleanup(func() { journalctlPath = previousJournalctl })
	collector := NewCoreLogCollector(map[core.Engine]EngineSpec{
		core.EngineSingBox: {Service: "qagent-sing-box.service"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("systemd reconnect fixture leaked")
		}
	}()
	if entry, ok := waitForLine(t, collector, "before journal reconnect"); !ok || entry.Engine != core.EngineSingBox {
		t.Fatalf("journal entry before reconnect = %+v, ok=%v", entry, ok)
	}
	if entry, ok := waitForLine(t, collector, "after journal reconnect"); !ok || entry.Engine != core.EngineSingBox {
		t.Fatalf("journal entry after reconnect = %+v, ok=%v", entry, ok)
	}
}
