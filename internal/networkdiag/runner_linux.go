//go:build linux

// Package networkdiag executes a closed set of bounded node diagnostics.
package networkdiag

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const MaxOutput = 512 << 10
const NextTracePath = "/usr/local/lib/qagent/network-tools/nexttrace"

// Capabilities never return credentials or local database paths.
func Capabilities(ctx context.Context) core.DiagnosticCapabilities {
	c := core.DiagnosticCapabilities{Tools: map[string]core.DiagnosticTool{}, Providers: map[string]string{}, Addresses: []string{}}
	c.Tools["ping"] = core.DiagnosticTool{Version: "QControlHub ICMP v1"}
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		c.Tools["ping"] = core.DiagnosticTool{Error: "缺少 ICMP 原始套接字权限"}
	} else {
		conn.Close()
	}
	for name, path := range map[string]string{"nexttrace": NextTracePath, "iperf3": "iperf3"} {
		local, cancel := context.WithTimeout(ctx, 2*time.Second)
		args := []string{"--version"}
		if name == "nexttrace" {
			args = []string{"--no-color", "--version"}
		}
		b, e := exec.CommandContext(local, path, args...).CombinedOutput()
		cancel()
		t := core.DiagnosticTool{}
		switch {
		case e != nil:
			switch {
			case errors.Is(e, exec.ErrNotFound), errors.Is(e, os.ErrNotExist):
				t.AutoInstall = os.Geteuid() == 0
				t.Error = diagnosticToolName(name) + " 未安装"
				if !t.AutoInstall {
					t.Error += "，Agent 无安装权限；请管理员以 root 准备依赖后重启 Agent"
				}
			case errors.Is(e, os.ErrPermission):
				t.Error = diagnosticToolName(name) + " 无执行权限；请节点管理员检查工具权限后重启 Agent"
			default:
				t.Error = diagnosticToolName(name) + " 版本检测失败；请节点管理员检查工具能否执行后重启 Agent"
			}
		case name == "nexttrace" && !strings.Contains(string(b), "NextTrace v1.7.6"):
			t.Error = "NextTrace 版本不兼容，需要 v1.7.6；请节点管理员检查私有工具路径后执行 qagent prepare-network-tools，再重启 Agent"
		default:
			t.Version = strings.Split(strings.TrimSpace(string(b)), "\n")[0]
		}
		if name == "iperf3" && t.Error == "" {
			local, cancel := context.WithTimeout(ctx, 2*time.Second)
			help, e := exec.CommandContext(local, path, "--help").CombinedOutput()
			cancel()
			for _, flag := range []string{"--idle-timeout", "--server-bitrate-limit", "--connect-timeout", "--get-server-output", "--json-stream", "--forceflush"} {
				if e != nil || !strings.Contains(string(help), flag) {
					t.Error = "iperf3 版本不兼容，缺少 " + flag + "；请节点管理员升级至支持该选项的版本（已验证 3.20）后重启 Agent"
					break
				}
			}
		}
		c.Tools[name] = t
	}
	for _, p := range []string{"NextTrace-API", "IP.SB", "IPAPI.com", "IPInfo", "IPInsight", "disable-geoip", "DN42"} {
		c.Providers[p] = ""
	}
	c.Providers["ipdb.one"] = "节点未配置 API ID 和密钥"
	if os.Getenv("IPDBONE_API_ID") != "" && os.Getenv("IPDBONE_API_KEY") != "" {
		c.Providers["ipdb.one"] = ""
	}
	c.Providers["IPInfoLocal"] = "节点未配置可读本地数据库"
	for _, p := range localDatabasePaths() {
		if f, e := maxminddb.Open(p); e == nil {
			f.Close()
			c.Providers["IPInfoLocal"] = ""
			break
		}
	}
	c.Providers["chunzhen"] = "本地查询服务不可用"
	endpoint := os.Getenv("NEXTTRACE_CHUNZHENURL")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:2060"
	}
	if endpoint != "" {
		c.Providers["chunzhen"] = "本地查询服务不可用"
		if u, e := url.Parse(endpoint); e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			values := u.Query()
			values.Set("ip", "1.1.1.1")
			u.RawQuery = values.Encode()
			local, cancel := context.WithTimeout(ctx, time.Second)
			request, e := http.NewRequestWithContext(local, http.MethodGet, u.String(), nil)
			if e == nil {
				client := http.Client{Timeout: time.Second}
				response, e := client.Do(request)
				if e == nil {
					var record map[string]json.RawMessage
					if response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&record) == nil && len(record["1.1.1.1"]) > 0 {
						c.Providers["chunzhen"] = ""
					}
					response.Body.Close()
				}
			}
			cancel()
		}
	}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := iface.Addrs()
		for _, a := range addresses {
			ip, _, e := net.ParseCIDR(a.String())
			if e != nil {
				continue
			}
			s := ip.String()
			if _, e := core.DiagnosticAddress(s); e == nil {
				c.Addresses = append(c.Addresses, s)
			}
		}
	}
	return c
}

type limitedBuffer struct {
	b     bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.b.Len()+n > b.limit {
		return 0, errors.New("诊断输出超过限制")
	}
	return b.b.Write(p)
}

func Args(c core.DiagnosticCommand) (string, []string, error) {
	a, e := core.DiagnosticAddress(c.Target.Address)
	if e != nil {
		return "", nil, e
	}
	s, e := core.DiagnosticAddress(c.Source.Address)
	if e != nil || s.Is6() != a.Is6() {
		return "", nil, errors.New("两端地址族不一致")
	}
	p := c.Parameters
	if e = p.Validate(a.Is6()); e != nil {
		return "", nil, e
	}
	if c.Role != "probe" && c.Role != "server" && c.Role != "client" {
		return "", nil, errors.New("无效执行角色")
	}
	family := "-4"
	if a.Is6() {
		family = "-6"
	}
	n := strconv.Itoa
	if p.Tool == "iperf3" {
		if c.ByteLimit < 0 || c.ByteLimit > p.TrafficReservation()/2 {
			return "", nil, errors.New("共享测速流量预算无效")
		}
		args := []string{family, "--json", "--bind", s.String(), "--port", n(p.Port)}
		if c.Role == "server" {
			args = append(args, "--server", "--one-off", "--idle-timeout", "10")
			rate := p.Bandwidth
			if rate == 0 && p.Protocol == "udp" {
				rate = 1000000
			}
			if rate > 0 {
				args = append(args, "--server-bitrate-limit", strconv.FormatInt(rate*int64(p.Streams)*2, 10))
			}
			return "iperf3", args, nil
		}
		args = append(args, "--client", a.String(), "--parallel", n(p.Streams), "--length", n(p.Size), "--connect-timeout", "5000", "--get-server-output", "--json-stream", "--forceflush", "--interval", "1")
		if c.ByteLimit > 0 {
			args = append(args, "--bytes", strconv.FormatInt(c.ByteLimit, 10))
		} else {
			args = append(args, "--time", n(p.Duration))
		}
		if p.Bandwidth > 0 {
			args = append(args, "--bitrate", strconv.FormatInt(p.Bandwidth, 10))
		}
		if p.Protocol == "udp" {
			args = append(args, "--udp")
		}
		return "iperf3", args, nil
	}
	if p.Tool == "ping" {
		return "", nil, nil
	}
	args := []string{family, "--json", "--no-color", "--no-rdns", "--map", "--source", s.String(), "--queries", n(p.Count), "--ttl-time", n(p.IntervalMS), "--timeout", n(p.TimeoutMS), "--max-hops", n(p.MaxHops), "--psize", n(p.Size), "--parallel-requests", "1"}
	if p.Tool == "mtr" {
		args = args[:len(args)-2]
		args = append(args, "--mtr", "--report", "--parallel-requests", n(p.MaxHops))
	} else {
		args = append(args, "--traceroute")
	}
	if p.Protocol != "icmp" {
		args = append(args, "--"+p.Protocol, "--port", n(p.Port), "--source-port", "-1")
	}
	return NextTracePath, append(args, "--data-provider", p.Provider, a.String()), nil
}

// runProcess uses a process group and a parent-death signal. A killed or
// re-executed Agent cannot leave a listening server behind. Cancellation is
// independent of taskLifecycleMu and WSS result delivery.
func runProcess(ctx context.Context, c core.DiagnosticCommand, ready func(), launched func(int) error, progress ...func(json.RawMessage)) core.DiagnosticResult {
	r := core.DiagnosticResult{ID: c.ID, State: "failed", Cleaned: true, At: time.Now().UTC(), ExitCode: -1}
	path, args, err := Args(c)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	if c.Parameters.Tool == "ping" {
		return ping(ctx, c)
	}
	streaming := c.Parameters.Tool == "traceroute" || c.Parameters.Tool == "mtr"
	if streaming {
		path, err = os.Executable()
		if err != nil {
			r.Error = "无法定位 Agent 执行文件"
			return r
		}
		args = []string{"network-trace-stream"}
	}
	cmd := exec.CommandContext(ctx, path, args...)
	if streaming {
		body, _ := json.Marshal(c)
		cmd.Stdin = bytes.NewReader(body)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	// Inherit only operator-supplied NextTrace settings. Disable implicit loops,
	// proxy inheritance and interactive token discovery.
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "HOME=/nonexistent", "NO_COLOR=1"}
	for _, key := range []string{"NEXTTRACE_TOKEN", "NEXTTRACE_API_V4_TOKEN", "NEXTTRACE_IPINFO_TOKEN", "NEXTTRACE_IPINSIGHT_TOKEN", "NEXTTRACE_IPINFOLOCALPATH", "NEXTTRACE_CHUNZHENURL", "IPDBONE_BASE_URL", "IPDBONE_API_ID", "IPDBONE_API_KEY"} {
		if v := os.Getenv(key); v != "" {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	out := &limitedBuffer{limit: MaxOutput}
	stderr := &limitedBuffer{limit: 8192}
	cmd.Stdout = out
	stream := &snapshotWriter{}
	if streaming {
		if len(progress) > 0 {
			stream.publish = progress[0]
		}
		cmd.Stdout = stream
	}
	iperfStream := &iperfStreamWriter{}
	if c.Parameters.Tool == "iperf3" && c.Role == "client" {
		if len(progress) > 0 {
			iperfStream.publish = progress[0]
		}
		cmd.Stdout = iperfStream
	}
	cmd.Stderr = stderr
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 2 * time.Second
	if err = cmd.Start(); err != nil {
		r.Error = diagnosticToolName(c.Parameters.Tool) + " 启动失败：" + err.Error()
		return r
	}
	if err = launched(cmd.Process.Pid); err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		r.Error = "无法持久保存进程归属"
		return r
	}
	done := make(chan error, 1)
	processDone := make(chan struct{})
	var durationEnded atomic.Bool
	go func() { err := cmd.Wait(); close(processDone); done <- err }()
	// A shared full-speed test ends on its byte allowance or wall-clock bound.
	// SIGINT lets iperf3 write its genuine partial/final JSON statistics. This
	// timer is local and independent of the browser/control-plane connection.
	if c.Role == "client" && c.ByteLimit > 0 {
		timer := time.AfterFunc(time.Duration(c.Parameters.Duration)*time.Second, func() {
			select {
			case <-processDone:
				return
			default:
			}
			durationEnded.Store(true)
			_ = cmd.Process.Signal(syscall.SIGINT)
			grace := time.NewTimer(2 * time.Second)
			defer grace.Stop()
			select {
			case <-processDone:
				return
			case <-grace.C:
				_ = cmd.Process.Kill()
			}
		})
		defer timer.Stop()
	}
	if c.Role == "server" {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
	waitReady:
		for {
			select {
			case err = <-done:
				r.Error = "测速服务端在就绪前退出"
				goto finished
			case <-ticker.C:
				if ownsListener(cmd.Process.Pid, c.Parameters.Port) {
					ready()
					break waitReady
				}
			case <-timer.C:
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				err = <-done
				r.Error = "测速服务端就绪超时"
				goto finished
			case <-ctx.Done():
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				err = <-done
				goto finished
			}
		}
	}
	err = <-done
finished:
	// Wait has reaped this exact child. Never kill by name or by a reused PID.
	r.ExitCode = cmd.ProcessState.ExitCode()
	r.Stderr = core.DiagnosticText(stderr.b.String())
	for _, key := range []string{"NEXTTRACE_TOKEN", "NEXTTRACE_API_V4_TOKEN", "NEXTTRACE_IPINFO_TOKEN", "NEXTTRACE_IPINSIGHT_TOKEN", "IPDBONE_API_ID", "IPDBONE_API_KEY"} {
		if secret := os.Getenv(key); secret != "" {
			r.Stderr = strings.ReplaceAll(r.Stderr, secret, "[redacted]")
		}
	}
	if len(iperfStream.latest) > 0 {
		out.b.Write(iperfStream.latest)
	}
	if streaming && len(stream.latest) > 0 {
		out.b.Write(stream.latest)
	}
	if durationEnded.Load() && ctx.Err() == nil && c.Parameters.Tool == "iperf3" && json.Valid(out.b.Bytes()) {
		var report map[string]json.RawMessage
		_ = json.Unmarshal(out.b.Bytes(), &report)
		var end map[string]json.RawMessage
		_ = json.Unmarshal(report["end"], &end)
		var message string
		_ = json.Unmarshal(report["error"], &message)
		if (len(end["sum_sent"]) > 0 || len(end["sum_received"]) > 0 || len(end["sum"]) > 0) && (message == "" || strings.Contains(message, "interrupt")) {
			delete(report, "error")
			report["limit_reason"] = json.RawMessage(`"duration"`)
			body, _ := json.Marshal(report)
			out.b.Reset()
			out.b.Write(body)
			err = nil
		}
	}
	if json.Valid(out.b.Bytes()) {
		r.Output = append(json.RawMessage(nil), out.b.Bytes()...)
		var decoded any
		if json.Unmarshal(r.Output, &decoded) == nil {
			r.Output, _ = json.Marshal(redactProviderSecrets(core.RedactDiagnosticValue(decoded)))
		}
		if c.Parameters.Tool == "iperf3" {
			var report struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(r.Output, &report) == nil && report.Error != "" {
				r.Error = core.DiagnosticText(report.Error)
			}
		}
		if c.Parameters.Tool == "mtr" {
			var report struct {
				Schema int             `json:"schema_version"`
				Stats  json.RawMessage `json:"stats"`
			}
			if json.Unmarshal(r.Output, &report) != nil || report.Schema != 1 || len(report.Stats) == 0 {
				r.Error = "NextTrace MTR 输出契约不兼容"
			}
		}
		if c.Parameters.Tool == "traceroute" {
			var report map[string]json.RawMessage
			_ = json.Unmarshal(r.Output, &report)
			if _, ok := report["Hops"]; !ok {
				r.Error = "NextTrace 路由输出契约不兼容"
			}
		}
	} else if out.b.Len() > 0 {
		r.Error = "工具输出不符合 JSON 契约"
	}
	if err != nil {
		if r.Error == "" {
			r.Error = fmt.Sprintf("工具退出码 %d", r.ExitCode)
		}
	} else if c.Parameters.Tool == "iperf3" && c.Role == "client" && iperfStream.report["end"] == nil {
		r.Error = "iperf3 未返回完整结束结果"
	} else if streaming && (len(stream.latest) == 0 || stream.partial) {
		r.Error = "NextTrace 未返回完整结束结果"
	} else if r.Error == "" {
		r.State = "succeeded"
	}
	if ctx.Err() != nil {
		r.State = "canceled"
		r.Error = "测量已停止"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			r.State = "failed"
			r.Error = "测量执行超时"
		}
	}
	if r.Error != "" && ctx.Err() == nil {
		r.Error = diagnosticFailure(c, r.Error, r.Stderr, r.ExitCode)
	}
	r.At = time.Now().UTC()
	return r
}

func diagnosticToolName(tool string) string {
	switch tool {
	case "nexttrace", "traceroute":
		return "NextTrace"
	case "mtr":
		return "NextTrace MTR"
	case "iperf3":
		return "iperf3"
	default:
		return "Ping"
	}
}

// Summarize known failures without guessing reachability from a silent UDP
// probe. Bounded, redacted tool diagnostics remain available separately.
func diagnosticFailure(c core.DiagnosticCommand, message, stderr string, exit int) string {
	text := strings.ToLower(message + "\n" + stderr)
	name := diagnosticToolName(c.Parameters.Tool)
	switch {
	case strings.Contains(text, "connection refused"):
		return name + " 连接被拒绝；请确认目标服务端、端口和防火墙允许 TCP 控制连接"
	case strings.Contains(text, "address already in use"):
		return name + " 端口已被占用；请选择其他已授权端口，不会关闭已有服务"
	case strings.Contains(text, "network is unreachable"), strings.Contains(text, "no route to host"):
		return name + " 无法连接目标网络；请检查所选地址族、节点路由和网络连通性"
	case strings.Contains(text, "permission denied"), strings.Contains(text, "operation not permitted"):
		return name + " 权限不足；请节点管理员检查 Agent 的执行权限和原始套接字能力"
	case strings.Contains(text, "dns resolution failed"), strings.Contains(text, "no such host"):
		return name + " 域名解析失败；请检查节点 DNS 和查询服务网络，已获得的探测结果仍会保留"
	case c.Parameters.Tool == "iperf3" && (strings.Contains(text, "timed out") || strings.Contains(text, "timeout")):
		return "iperf3 连接或测试超时；请检查目标端口和 TCP 控制连接，UDP 测试也需要该连接"
	case strings.HasPrefix(message, "工具退出码 "):
		return fmt.Sprintf("%s 执行失败（退出码 %d）；请展开工具诊断查看详情", name, exit)
	default:
		return message
	}
}

// Check the child's own socket inode, without consuming an iperf one-off
// control connection or confusing another process's listener with readiness.
func ownsListener(pid, port int) bool {
	fds, _ := filepath.Glob(fmt.Sprintf("/proc/%d/fd/*", pid))
	sockets := map[string]bool{}
	for _, f := range fds {
		link, _ := os.Readlink(f)
		if strings.HasPrefix(link, "socket:[") {
			sockets[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = true
		}
	}
	for _, name := range []string{"tcp", "tcp6"} {
		b, _ := os.ReadFile("/proc/net/" + name)
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 10 || f[3] != "0A" || !sockets[f[9]] {
				continue
			}
			parts := strings.Split(f[1], ":")
			p, _ := strconv.ParseInt(parts[len(parts)-1], 16, 32)
			if int(p) == port {
				return true
			}
		}
	}
	return false
}

type pingReport struct {
	Sent        int     `json:"sent"`
	Received    int     `json:"received"`
	Loss        float64 `json:"loss_percent"`
	Min         float64 `json:"min_ms"`
	Avg         float64 `json:"avg_ms"`
	Max         float64 `json:"max_ms"`
	Stddev      float64 `json:"stdev_ms"`
	Reached     bool    `json:"reached"`
	Unreachable int     `json:"unreachable"`
}

func ping(ctx context.Context, c core.DiagnosticCommand) core.DiagnosticResult {
	r := core.DiagnosticResult{ID: c.ID, State: "failed", Cleaned: true, ExitCode: -1}
	p := c.Parameters
	protocol, network, requestType, replyType := 1, "ip4:icmp", icmp.Type(ipv4.ICMPTypeEcho), icmp.Type(ipv4.ICMPTypeEchoReply)
	target, _ := core.DiagnosticAddress(c.Target.Address)
	if target.Is6() {
		protocol = 58
		network = "ip6:ipv6-icmp"
		requestType = ipv6.ICMPTypeEchoRequest
		replyType = ipv6.ICMPTypeEchoReply
	}
	conn, err := icmp.ListenPacket(network, c.Source.Address)
	if err != nil {
		r.Error = "ICMP 套接字创建失败：" + err.Error()
		return r
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()
	report := pingReport{}
	var sum, sum2 float64
	var randomID [2]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		r.Error = "无法分配 ICMP 标识"
		return r
	}
	id := int(binary.BigEndian.Uint16(randomID[:]))
	// Payload includes a per-session marker so unrelated echo replies cannot
	// satisfy a sample, even if two Agent processes share an ICMP identifier.
	marker := []byte(c.ID)
	payload := make([]byte, p.Size)
	copy(payload, marker)
	buf := make([]byte, 65536)
	for seq := 1; seq <= p.Count && ctx.Err() == nil; seq++ {
		start := time.Now()
		m := icmp.Message{Type: requestType, Body: &icmp.Echo{ID: id, Seq: seq, Data: payload}}
		b, e := m.Marshal(nil)
		if e != nil {
			err = e
			break
		}
		if _, e = conn.WriteTo(b, &net.IPAddr{IP: net.ParseIP(c.Target.Address)}); e != nil {
			err = e
			break
		}
		report.Sent++
		_ = conn.SetReadDeadline(start.Add(time.Duration(p.TimeoutMS) * time.Millisecond))
		for {
			n, peer, e := conn.ReadFrom(buf)
			if e != nil {
				break
			}
			reply, e := icmp.ParseMessage(protocol, buf[:n])
			if e != nil {
				continue
			}
			if unreachableReply(reply, protocol, id, seq, net.ParseIP(c.Target.Address)) {
				report.Unreachable++
				break
			}
			if peer.String() != c.Target.Address || reply.Type != replyType {
				continue
			}
			echo, ok := reply.Body.(*icmp.Echo)
			if !ok || echo.ID != id || echo.Seq != seq || !bytes.Equal(echo.Data, payload) {
				continue
			}
			ms := float64(time.Since(start)) / float64(time.Millisecond)
			report.Received++
			sum += ms
			sum2 += ms * ms
			if report.Received == 1 || ms < report.Min {
				report.Min = ms
			}
			if ms > report.Max {
				report.Max = ms
			}
			break
		}
		wait := time.Until(start.Add(time.Duration(p.IntervalMS) * time.Millisecond))
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	if report.Sent > 0 {
		report.Loss = 100 * float64(report.Sent-report.Received) / float64(report.Sent)
	}
	if report.Received > 0 {
		report.Reached = true
		report.Avg = sum / float64(report.Received)
		report.Stddev = math.Sqrt(math.Max(0, sum2/float64(report.Received)-report.Avg*report.Avg))
	}
	if report.Unreachable > 0 && report.Received == 0 {
		err = errors.New("目标返回 ICMP 不可达")
	}
	r.Output, _ = json.Marshal(report)
	r.State = "succeeded"
	r.ExitCode = 0
	if err != nil {
		r.State = "failed"
		r.Error = err.Error()
		r.ExitCode = 1
	}
	if ctx.Err() != nil {
		r.State = "canceled"
		r.Error = "测量已停止"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			r.State = "failed"
			r.Error = "测量执行超时"
		}
	}
	r.At = time.Now().UTC()
	return r
}

// Manager persists admission before execution and terminal results before
// delivery. Replayed commands, including across an Agent restart, never run
// twice. An incomplete journal entry after restart is a failed measurement.
type Manager struct {
	Prepare func(context.Context, string) error
	mu      sync.Mutex
	path    string
	entries map[string]*entry
	life    *sync.Mutex
	ctx     context.Context
	lock    *os.File
	wg      sync.WaitGroup
}
type entry struct {
	BootID       string                 `json:"boot_id,omitempty"`
	PID          int                    `json:"pid,omitempty"`
	ProcessStart string                 `json:"process_start,omitempty"`
	Command      core.DiagnosticCommand `json:"command"`
	Result       core.DiagnosticResult  `json:"result"`
	cancel       context.CancelFunc
	renewed      time.Time
}

func New(ctx context.Context, path string, life *sync.Mutex) (*Manager, error) {
	m := &Manager{path: path, entries: map[string]*entry{}, life: life, ctx: ctx}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("诊断状态路径必须是绝对路径")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	owner, okOwner := parent.Sys().(*syscall.Stat_t)
	if !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 || parent.Mode().Perm()&0022 != 0 || !okOwner || owner.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("诊断状态目录归属或权限不安全")
	}
	for _, name := range []string{path, path + ".lock", path + ".tmp"} {
		info, e := os.Lstat(name)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, e
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ok || owner.Uid != uint32(os.Geteuid()) {
			return nil, errors.New("诊断状态文件归属或权限不安全")
		}
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("诊断状态已被另一个 Agent 使用")
	}
	m.lock = lock
	ok := false
	defer func() {
		if !ok {
			lock.Close()
		}
	}()

	if info, e := os.Stat(path); e == nil && info.Size() > 70<<20 {
		return nil, errors.New("诊断日志超过限制")
	}
	if b, e := os.ReadFile(path); e == nil {
		if e = json.Unmarshal(b, &m.entries); e != nil {
			return nil, e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	for _, e := range m.entries {
		if !e.Result.Terminal() || !e.Result.Cleaned {
			e.Result.State = "failed"
			e.Result.Error = "Agent 重启中断测量"
			e.Result.Cleaned = reclaim(e)
			e.Result.At = time.Now().UTC()
		}
	}
	if e := m.save(); e != nil {
		return nil, e
	}
	ok = true
	go m.watch()
	return m, nil
}
func (m *Manager) save() error {
	b, e := json.Marshal(m.entries)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(m.path), 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(m.path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(m.path+".tmp", m.path); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(m.path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func (m *Manager) Apply(commands []core.DiagnosticCommand) []core.DiagnosticResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	results := []core.DiagnosticResult{}
	for _, c := range commands {
		e, exists := m.entries[c.ID]
		if exists {
			expected := e.Command
			received := c
			received.Role = expected.Role
			eb, _ := json.Marshal(expected)
			rb, _ := json.Marshal(received)
			if !bytes.Equal(eb, rb) {
				results = append(results, core.DiagnosticResult{ID: c.ID, State: "failed", Error: "重复标识的参数不一致", Cleaned: e.Result.Cleaned, At: now})
				continue
			}
			e.renewed = now
			if e.Result.Terminal() && !e.Result.Cleaned {
				e.Result.Cleaned = reclaim(e)
				_ = m.save()
			}
			if c.Role == "stop" && e.cancel != nil {
				e.cancel()
			}
			results = append(results, e.Result)
			continue
		}
		if c.Role == "stop" {
			r := core.DiagnosticResult{ID: c.ID, State: "canceled", Cleaned: true, At: now}
			if len(m.entries) < 128 && len(c.ID) <= 100 && strings.HasPrefix(c.ID, "ndg_") {
				m.entries[c.ID] = &entry{Command: c, Result: r, renewed: now}
				if err := m.save(); err != nil {
					delete(m.entries, c.ID)
					r.State = "failed"
					r.Error = "无法保存停止记录"
				}
			}
			results = append(results, r)
			continue
		}
		r := core.DiagnosticResult{ID: c.ID, State: "running", At: now}
		invalid := func(message string) { r.State = "failed"; r.Error = message; r.Cleaned = true }
		if len(c.ID) > 100 || !strings.HasPrefix(c.ID, "ndg_") || c.SessionID == "" || c.Deadline.Before(now) || c.Deadline.After(now.Add(12*time.Minute)) {
			invalid("诊断标识或期限无效")
		}
		if _, _, err := Args(c); err != nil {
			invalid(err.Error())
		}
		local := false
		interfaces, _ := net.Interfaces()
		for _, iface := range interfaces {
			addresses, _ := iface.Addrs()
			for _, address := range addresses {
				ip, _, _ := net.ParseCIDR(address.String())
				if ip != nil && ip.String() == c.Source.Address {
					local = true
				}
			}
		}
		if !local {
			invalid("源地址未分配到此节点")
		}
		if len(m.entries) >= 128 {
			invalid("诊断执行记录已满，请稍后重试")
			results = append(results, r)
			continue
		}
		busy := false
		for _, other := range m.entries {
			if !other.Result.Terminal() || !other.Result.Cleaned {
				busy = true
			}
		}
		if busy {
			invalid("节点正在执行其他网络诊断")
		}
		reserved := false
		if !r.Terminal() {
			if !m.life.TryLock() {
				invalid("节点正在执行部署或升级")
			} else {
				// Transfer the acquired lock to run, without an admission/start
				// gap in which an ordinary deployment could make stop wait.
				reserved = true
			}
		}
		e = &entry{Command: c, Result: r, renewed: now}
		m.entries[c.ID] = e
		if err := m.save(); err != nil {
			if reserved {
				m.life.Unlock()
			}
			delete(m.entries, c.ID)
			r.State = "failed"
			r.Error = "无法持久保存执行记录"
			r.Cleaned = true
			results = append(results, r)
			continue
		}
		if !r.Terminal() {
			ctx, cancel := context.WithDeadline(m.ctx, c.Deadline)
			e.cancel = cancel
			m.wg.Add(1)
			go m.run(ctx, c)
		}
		results = append(results, r)
	}
	return results
}
func (m *Manager) run(ctx context.Context, c core.DiagnosticCommand) {
	defer m.wg.Done()
	// Non-server probes hold the existing lifecycle lock. Server readiness
	// releases admission immediately, allowing peer clients and stop controls.
	locked := true
	defer func() {
		if locked {
			m.life.Unlock()
		}
	}()
	r := func() core.DiagnosticResult {
		if m.Prepare != nil && c.Parameters.Tool != "ping" {
			tool := c.Parameters.Tool
			if tool == "traceroute" || tool == "mtr" {
				tool = "nexttrace"
			}
			capability := Capabilities(ctx).Tools[tool]
			if capability.AutoInstall {
				m.mu.Lock()
				e := m.entries[c.ID]
				e.Result.Stage = "installing"
				e.Result.Sequence++
				err := m.save()
				m.mu.Unlock()
				if err == nil {
					err = m.Prepare(ctx, tool)
				}
				if err == nil {
					capability = Capabilities(ctx).Tools[tool]
					if capability.Error != "" {
						err = errors.New(capability.Error)
					}
				}
				if err != nil {
					state := "failed"
					message := "自动安装诊断依赖失败：" + core.DiagnosticText(err.Error()) + "；请检查节点软件源、下载连通性及安装权限后重试"
					if errors.Is(ctx.Err(), context.Canceled) {
						state = "canceled"
						message = "测量已停止"
					}
					if errors.Is(ctx.Err(), context.DeadlineExceeded) {
						message = "诊断依赖安装超时；请检查节点软件源及下载连通性后重试"
					}
					return core.DiagnosticResult{ID: c.ID, State: state, Cleaned: true, Error: message, At: time.Now().UTC(), ExitCode: -1}
				}
				m.mu.Lock()
				e.Result.Stage = ""
				e.Result.Sequence++
				err = m.save()
				m.mu.Unlock()
				if err != nil {
					return core.DiagnosticResult{ID: c.ID, State: "failed", Cleaned: true, Error: "依赖准备状态保存失败", At: time.Now().UTC(), ExitCode: -1}
				}
			} else if capability.Error != "" {
				return core.DiagnosticResult{ID: c.ID, State: "failed", Cleaned: true, Error: capability.Error, At: time.Now().UTC(), ExitCode: -1}
			}
		}
		// Only preparation needs the lifecycle lock on a receiver. Its ready
		// listener must not block independent stop or the peer client.
		if c.Role == "server" {
			m.life.Unlock()
			locked = false
		}
		return runProcess(ctx, c, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			e := m.entries[c.ID]
			e.Result.State = "ready"
			e.Result.At = time.Now().UTC()
			if err := m.save(); err != nil {
				e.cancel()
			}
		}, func(pid int) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			e := m.entries[c.ID]
			e.PID = pid
			e.BootID = bootID()
			e.ProcessStart = processStart(pid)
			if e.ProcessStart == "" || e.BootID == "" {
				return errors.New("process identity unavailable")
			}
			return m.save()
		}, func(output json.RawMessage) {
			m.mu.Lock()
			defer m.mu.Unlock()
			e := m.entries[c.ID]
			e.Result.Output = output
			e.Result.At = time.Now().UTC()
			e.Result.Sequence++
			if err := m.save(); err != nil {
				e.cancel()
			}
		})
	}()
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[c.ID]
	r.Sequence = e.Result.Sequence + 1
	e.Result = r
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	if err := m.save(); err != nil {
		e.Result.State = "failed"
		e.Result.Error = "结果持久化失败"
	}
}
func (m *Manager) watch() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			m.mu.Lock()
			for _, e := range m.entries {
				if e.cancel != nil {
					e.cancel()
				}
			}
			m.mu.Unlock()
			m.wg.Wait()
			m.lock.Close()
			return
		case <-t.C:
			m.mu.Lock()
			changed := false
			for id, e := range m.entries {
				if e.cancel != nil && time.Since(e.renewed) > 10*time.Second {
					e.cancel()
				}
				if e.Result.Terminal() && e.Result.Cleaned && time.Since(e.Result.At) > 24*time.Hour {
					delete(m.entries, id)
					changed = true
				}
			}
			if changed {
				_ = m.save()
			}
			m.mu.Unlock()
		}
	}
}

var _ io.Writer = (*limitedBuffer)(nil)

func processStart(pid int) string {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return ""
	}
	at := strings.LastIndexByte(string(b), ')')
	if at < 0 {
		return ""
	}
	fields := strings.Fields(string(b)[at+1:])
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}
func reclaim(e *entry) bool {
	if e.PID > 0 && e.BootID == "" {
		return false
	}
	if e.BootID != "" && e.BootID != bootID() {
		return true
	}
	if e.PID <= 0 || e.ProcessStart == "" || processStart(e.PID) != e.ProcessStart {
		return true
	}
	_ = syscall.Kill(-e.PID, syscall.SIGKILL)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if processStart(e.PID) != e.ProcessStart || processZombie(e.PID) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func processZombie(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	at := strings.LastIndexByte(string(b), ')')
	return at >= 0 && strings.HasPrefix(string(b)[at+1:], " Z ")
}

// Active is consulted under the existing lifecycle mutex. A dedicated server
// releases that mutex but still prevents a deployment/upgrade from overlapping.
func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.entries {
		if !e.Result.Terminal() || !e.Result.Cleaned {
			return true
		}
	}
	return false
}

func unreachableReply(message *icmp.Message, protocol, id, seq int, target net.IP) bool {
	if message.Type != ipv4.ICMPTypeDestinationUnreachable && message.Type != ipv6.ICMPTypeDestinationUnreachable {
		return false
	}
	body, ok := message.Body.(*icmp.DstUnreach)
	if !ok {
		return false
	}
	data := body.Data
	offset := 40
	if protocol == 1 {
		if len(data) < 20 || data[0]>>4 != 4 || !net.IP(data[16:20]).Equal(target) {
			return false
		}
		offset = int(data[0]&15) * 4
		if offset < 20 {
			return false
		}
	} else if len(data) < 40 || data[0]>>4 != 6 || !net.IP(data[24:40]).Equal(target) {
		return false
	}
	if len(data) < offset+8 {
		return false
	}
	original, err := icmp.ParseMessage(protocol, data[offset:])
	if err != nil {
		return false
	}
	echo, ok := original.Body.(*icmp.Echo)
	return ok && echo.ID == id && echo.Seq == seq
}

func redactProviderSecrets(value any) any {
	switch v := value.(type) {
	case string:
		for _, key := range []string{"NEXTTRACE_TOKEN", "NEXTTRACE_API_V4_TOKEN", "NEXTTRACE_IPINFO_TOKEN", "NEXTTRACE_IPINSIGHT_TOKEN", "IPDBONE_API_ID", "IPDBONE_API_KEY"} {
			if secret := os.Getenv(key); secret != "" {
				v = strings.ReplaceAll(v, secret, "[redacted]")
			}
		}
		return v
	case map[string]any:
		for key, item := range v {
			v[key] = redactProviderSecrets(item)
		}
	case []any:
		for i, item := range v {
			v[i] = redactProviderSecrets(item)
		}
	}
	return value
}

func bootID() string {
	b, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Match the pinned upstream search order; no account can supply a file path.
func localDatabasePaths() []string {
	if p := os.Getenv("NEXTTRACE_IPINFOLOCALPATH"); p != "" {
		return []string{p}
	}
	paths := []string{"ipinfoLocal.mmdb", filepath.Join(filepath.Dir(NextTracePath), "ipinfoLocal.mmdb"), "/usr/local/share/nexttrace/ipinfoLocal.mmdb", "/usr/share/nexttrace/ipinfoLocal.mmdb"}
	return paths
}
