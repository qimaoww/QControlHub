//go:build linux

package networkdiag

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// iperfStreamWriter consumes iperf3's official NDJSON events. Keep actual
// interval sums and the upstream final report; never recompute final statistics.
type iperfStreamWriter struct {
	pending   []byte
	report    map[string]any
	intervals []any
	latest    json.RawMessage
	publish   func(json.RawMessage)
}

func (w *iperfStreamWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		end := len(p)
		if i >= 0 {
			end = i
		}
		if len(w.pending)+end > MaxOutput {
			return 0, errors.New("iperf3 event exceeds limit")
		}
		w.pending = append(w.pending, p[:end]...)
		if i < 0 {
			break
		}
		var event struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(w.pending, &event); err != nil {
			return 0, err
		}
		if w.report == nil {
			w.report = map[string]any{}
		}
		switch event.Event {
		case "start", "end":
			var data map[string]any
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return 0, err
			}
			// Avoid carrying connection cookies or remote system details into results.
			if event.Event == "start" {
				delete(data, "cookie")
				delete(data, "system_info")
			}
			w.report[event.Event] = data
		case "interval":
			var data struct {
				Sum json.RawMessage `json:"sum"`
			}
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return 0, err
			}
			if len(w.intervals) >= 64 {
				return 0, errors.New("iperf3 interval count exceeds limit")
			}
			var sum any
			if err := json.Unmarshal(data.Sum, &sum); err != nil {
				return 0, err
			}
			w.intervals = append(w.intervals, map[string]any{"sum": sum})
			w.report["intervals"] = w.intervals
		case "error":
			var message string
			if err := json.Unmarshal(event.Data, &message); err != nil {
				return 0, err
			}
			w.report["error"] = message
		default:
			w.pending = w.pending[:0]
			p = p[i+1:]
			continue
		}
		data, err := json.Marshal(redactProviderSecrets(core.RedactDiagnosticValue(w.report)))
		if err != nil {
			return 0, err
		}
		if len(data) > MaxOutput {
			return 0, errors.New("iperf3 output exceeds limit")
		}
		w.latest = data
		if w.publish != nil {
			w.publish(append(json.RawMessage(nil), data...))
		}
		w.pending = w.pending[:0]
		p = p[i+1:]
	}
	return n, nil
}
