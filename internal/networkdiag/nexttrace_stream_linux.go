//go:build linux

package networkdiag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/fatih/color"
	nxconfig "github.com/nxtrace/NTrace-core/config"
	"github.com/nxtrace/NTrace-core/ipgeo"
	"github.com/nxtrace/NTrace-core/trace"
	"github.com/nxtrace/NTrace-core/util"
	"github.com/nxtrace/NTrace-core/wshandle"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// RunNextTraceStream runs the pinned upstream engine in a dedicated child. This
// keeps its process globals and TCP listeners isolated from other Agent work.
// The upstream CLI's traceroute JSON is final-only; callbacks preserve the same
// structured hops (including Whois) without parsing terminal output.
func RunNextTraceStream(ctx context.Context, input io.Reader, output, diagnostics io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var c core.DiagnosticCommand
	if err := json.NewDecoder(io.LimitReader(input, 16384)).Decode(&c); err != nil {
		return err
	}
	if c.Parameters.Tool != "traceroute" && c.Parameters.Tool != "mtr" {
		return errors.New("unsupported streaming tool")
	}
	if _, _, err := Args(c); err != nil {
		return err
	}

	color.Output = diagnostics
	color.NoColor = true
	p := c.Parameters
	util.SuppressFastIPOutput = true
	if p.Provider == "DN42" {
		if err := nxconfig.InitConfigWithWriter(diagnostics); err != nil {
			return err
		}
	}
	if ipgeo.IsNextTraceAPIProvider(p.Provider) {
		if ipgeo.NextTraceAPIV4TokenConfigured() {
			_ = ipgeo.PrepareNextTraceAPIV4FastIP(ctx, false)
		} else {
			conn := wshandle.NewWithContextAsync(ctx)
			if conn != nil {
				defer conn.Close()
			}
		}
	}
	method := trace.Method(p.Protocol)
	target := net.ParseIP(c.Target.Address)
	size, err := trace.NormalizePacketSize(method, target, p.Size)
	if err != nil {
		return err
	}
	descriptor := ipgeo.GetSourceDescriptorSession(p.Provider)
	session := trace.CachedGeoSourceSession(descriptor)
	cfg := trace.Config{Context: ctx, OSType: 3, SrcAddr: c.Source.Address, SrcPort: -1,
		DstIP: target, DstPort: p.Port, BeginHop: 1, MaxHops: p.MaxHops, NumMeasurements: p.Count,
		ParallelRequests: 1, PacketInterval: 50, TTLInterval: p.IntervalMS,
		Timeout: time.Duration(p.TimeoutMS) * time.Millisecond, PktSize: size.PayloadSize,
		Lang: "cn", DN42: p.Provider == "DN42", IPGeoSource: session.Source,
		IPGeoDescriptor: descriptor.Current, RefreshIPGeoSource: session.Refresh}
	cfg, err = trace.NormalizeExplicitSourceConfig(method, cfg)
	if err != nil {
		return err
	}
	util.SrcPort = cfg.SrcPort
	util.DstIP = target.String()
	var mu sync.Mutex
	var writeErr error
	var last time.Time
	emit := func(value any, partial bool) {
		if partial && time.Since(last) < 250*time.Millisecond {
			return
		}
		// Upstream represents not-yet-filled TTL slots as nil slices. Emit empty
		// arrays so older open browser tabs also tolerate live snapshots.
		if res, ok := value.(*trace.Result); ok {
			hops := make([][]trace.Hop, len(res.Hops))
			for i, row := range res.Hops {
				if row == nil {
					hops[i] = []trace.Hop{}
				} else {
					hops[i] = row
				}
			}
			value = struct {
				Hops        [][]trace.Hop
				StopReason  *trace.StopReason `json:",omitempty"`
				TraceMapUrl string
			}{hops, res.StopReason, res.TraceMapUrl}
		}
		data, e := json.Marshal(value)
		if e == nil && len(data) > MaxOutput {
			e = errors.New("diagnostic snapshot exceeds limit")
		}
		if e == nil {
			e = json.NewEncoder(output).Encode(struct {
				Output  json.RawMessage `json:"output"`
				Partial bool            `json:"partial"`
			}{data, partial})
		}
		if e != nil && writeErr == nil {
			writeErr = e
			cancel()
		}
		last = time.Now()
	}
	if p.Tool == "traceroute" {
		cfg.RealtimePrinter = func(res *trace.Result, ttl int) { mu.Lock(); defer mu.Unlock(); emit(res, true) }
		cfg.AsyncPrinter = func(res *trace.Result) { mu.Lock(); defer mu.Unlock(); emit(res, true) }
		res, runErr := trace.TracerouteWithContext(ctx, method, cfg)
		mu.Lock()
		defer mu.Unlock()
		if res != nil {
			emit(res, false)
		}
		if writeErr != nil {
			return writeErr
		}
		return runErr
	}
	// Every TTL participates in the same MTR session. A single global worker
	// would serialize all hops (and exhaust the deadline at 100 rounds).
	// Upstream owns scheduling, response correlation and per-responder stats.
	cfg.ParallelRequests = p.MaxHops
	// TCP/UDP probes use SrcPort=-1 above, giving each probe a random tuple.
	// The Agent still admits only one diagnostic session per node.
	report := struct {
		Schema    int                `json:"schema_version"`
		Iteration int                `json:"iteration"`
		Stats     []trace.MTRHopStat `json:"stats"`
		PathEnd   *trace.StopReason  `json:"path_end"`
		Partial   bool               `json:"partial"`
	}{Schema: 1, Stats: []trace.MTRHopStat{}, Partial: true}
	opts := trace.MTROptions{HopInterval: time.Duration(p.IntervalMS) * time.Millisecond, MaxPerHop: p.Count, ProgressThrottle: 250 * time.Millisecond, AsyncMetadata: true,
		OnPathEnd:    func(reason *trace.StopReason) { mu.Lock(); defer mu.Unlock(); report.PathEnd = reason },
		OnProbeError: func(e error) { fmt.Fprintln(diagnostics, e) }}
	err = trace.RunMTR(ctx, method, cfg, opts, func(iteration int, stats []trace.MTRHopStat) {
		mu.Lock()
		defer mu.Unlock()
		report.Stats = stats
		report.Iteration = iteration
		emit(report, true)
	})
	mu.Lock()
	defer mu.Unlock()
	report.Partial = false
	emit(report, false)
	if writeErr != nil {
		return writeErr
	}
	return err
}

// snapshotWriter consumes bounded NDJSON frames and retains only the latest
// snapshot. stdout diagnostics cannot be mistaken for probe data.
type snapshotWriter struct {
	pending []byte
	latest  json.RawMessage
	partial bool
	publish func(json.RawMessage)
}

func (w *snapshotWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		end := len(p)
		if i >= 0 {
			end = i
		}
		if len(w.pending)+end > MaxOutput+1024 {
			return 0, errors.New("diagnostic frame exceeds limit")
		}
		w.pending = append(w.pending, p[:end]...)
		if i < 0 {
			break
		}
		var frame struct {
			Output  json.RawMessage `json:"output"`
			Partial bool            `json:"partial"`
		}
		if json.Unmarshal(w.pending, &frame) == nil && json.Valid(frame.Output) && len(frame.Output) <= MaxOutput {
			var decoded any
			if json.Unmarshal(frame.Output, &decoded) == nil {
				w.latest, _ = json.Marshal(redactProviderSecrets(core.RedactDiagnosticValue(decoded)))
				w.partial = frame.Partial
				if w.publish != nil {
					w.publish(append(json.RawMessage(nil), w.latest...))
				}
			}
		}
		w.pending = w.pending[:0]
		p = p[i+1:]
	}
	return n, nil
}
