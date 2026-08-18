package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

type deploymentDetail struct {
	Protocol string
	Endpoint string
	Mode     string
}

type deploymentStatus struct {
	SavedConfigID string
	SavedVersion  int
	Drift         bool
	DriftLabel    string
	DriftDetail   string
}

type clientAccessProfile struct {
	Tag      string
	Protocol string
	Profile  serverconfig.ClientProfile
}

type clientAccessData struct {
	Address  string
	Source   string
	Profiles []clientAccessProfile
}

type clientAccessPageEntry struct {
	Agent  core.Agent
	Engine core.Engine
	Access clientAccessData
}

type clientAccessPageData struct {
	Entries       []clientAccessPageEntry
	AgentID       string
	Engine        core.Engine
	Query         string
	TotalProfiles int
	TotalNodes    int
}

type clientAddressCandidate struct {
	Address string
	Source  string
}

type taskDiagnostic struct {
	Title  string
	Advice string
}

func diagnoseTask(task core.Task) taskDiagnostic {
	if task.Status != core.TaskFailed {
		return taskDiagnostic{}
	}
	errorText := strings.ToLower(task.Error)
	switch {
	case strings.Contains(errorText, "rolled back") || strings.Contains(errorText, "previous configuration was restored"):
		return taskDiagnostic{Title: "变更失败，已自动回滚", Advice: "旧配置或旧二进制已经恢复；先查询服务状态，确认运行正常后再重试。"}
	case strings.Contains(errorText, "rejected the configuration"):
		return taskDiagnostic{Title: "配置未通过真实内核校验", Advice: "展开节点返回结果定位具体字段；修正并保存配置后，再使用当前配置重试。"}
	case strings.Contains(errorText, "execution lease expired") || strings.Contains(errorText, "did not report a result"):
		return taskDiagnostic{Title: "Agent 未在执行租约内回写", Advice: "确认节点在线且 WSS 稳定；同一任务达到重试上限后需要手动重试。"}
	case task.Action == core.ActionInstall && (strings.Contains(errorText, "eof") || strings.Contains(errorText, "timeout") || strings.Contains(errorText, "connection reset") || strings.Contains(errorText, "download")):
		return taskDiagnostic{Title: "官方发行包下载未完成", Advice: "可直接重试；Agent 会重新从官方 Release 下载并再次校验 SHA-256。"}
	case task.Action == core.ActionInstall:
		return taskDiagnostic{Title: "内核安装或切换失败", Advice: "展开结果确认下载、校验或重启阶段，再决定重试或先查询服务状态。"}
	case task.Action == core.ActionValidate || task.Action == core.ActionDeploy:
		return taskDiagnostic{Title: "配置任务执行失败", Advice: "展开节点返回结果确认失败阶段；修改配置后使用“使用当前配置重试”。"}
	case task.Action == core.ActionReadConfig:
		return taskDiagnostic{Title: "无法读取节点当前配置", Advice: "确认白名单配置路径存在、归属和权限安全，并能通过目标节点上的真实内核校验。"}
	default:
		return taskDiagnostic{Title: "节点操作执行失败", Advice: "展开节点返回结果并确认节点与服务状态后再重试。"}
	}
}

func deploymentStatusFor(saved core.Config, deployed core.Deployment) deploymentStatus {
	result := deploymentStatus{SavedConfigID: saved.ID, SavedVersion: saved.Version}
	if saved.ID == "" {
		return result
	}
	if deployed.ConfigID == "" || deployed.ConfigVersion == 0 {
		result.Drift = true
		result.DriftLabel = "已保存配置尚未部署"
		result.DriftDetail = fmt.Sprintf("待部署 v%d", saved.Version)
		return result
	}
	if deployed.ConfigID != saved.ID {
		result.Drift = true
		result.DriftLabel = "当前运行其他配置"
		result.DriftDetail = fmt.Sprintf("待部署已保存的 v%d", saved.Version)
		return result
	}
	if deployed.ConfigVersion < saved.Version {
		result.Drift = true
		result.DriftLabel = "已保存版本尚未部署"
		result.DriftDetail = fmt.Sprintf("待部署 v%d", saved.Version)
	}
	return result
}

func deploymentDetailFor(engine core.Engine, content string) (deploymentDetail, bool) {
	input, ok := serverconfig.Parse(engine, content)
	if !ok {
		return deploymentDetail{}, false
	}
	protocol, ok := serverconfig.FindProtocol(engine, input.Protocol)
	if !ok {
		return deploymentDetail{}, false
	}
	mode := deploymentTransportName(input.Transport)
	if input.RealityEnabled {
		mode += " · Reality"
	} else if input.TLSEnabled {
		mode += " · TLS"
	}
	return deploymentDetail{
		Protocol: protocol.Name,
		Endpoint: net.JoinHostPort(input.Listen, strconv.Itoa(input.Port)),
		Mode:     mode,
	}, true
}

// maxClientAccessCacheEntries bounds the client profile cache; when exceeded
// the cache is rebuilt rather than evicted entry by entry (LRU complexity is
// not worth it for a parse that only costs tens of microseconds).
const maxClientAccessCacheEntries = 1024

// clientAccessFor derives the client connection profiles for one deployed
// engine configuration. The result depends only on (agent, engine, content),
// so it is cached keyed by a content digest: configurations are immutable
// per revision, making the cache safe until the process restarts.
func (s *Server) clientAccessFor(agent core.Agent, engine core.Engine, content string) clientAccessData {
	cacheKey := clientAccessCacheKey(agent.ID, engine, content)
	s.clientAccessMu.RLock()
	cached, hit := s.clientAccessCache[cacheKey]
	s.clientAccessMu.RUnlock()
	if hit {
		return cached
	}

	inputs := serverconfig.ParseAll(engine, content)
	result := clientAccessData{}
	if len(inputs) > 0 {
		serverName := firstAgentLabel(agent, "tls_server_name", "server_name")
		for _, candidate := range clientAddressCandidates(agent) {
			profiles := make([]clientAccessProfile, 0, len(inputs))
			for _, input := range inputs {
				profile, err := serverconfig.BuildClientProfile(input, candidate.Address, serverName)
				if err != nil {
					continue
				}
				protocol, ok := serverconfig.FindProtocol(engine, input.Protocol)
				if !ok {
					continue
				}
				profiles = append(profiles, clientAccessProfile{Tag: input.Tag, Protocol: protocol.Name, Profile: profile})
			}
			if len(profiles) > 0 {
				result = clientAccessData{Address: candidate.Address, Source: candidate.Source, Profiles: profiles}
				break
			}
		}
	}

	s.clientAccessMu.Lock()
	if len(s.clientAccessCache) >= maxClientAccessCacheEntries {
		// Bounded cache: drop everything rather than implementing LRU; the
		// parse cost is small and this only happens after thousands of
		// distinct (agent, engine, content) combinations.
		s.clientAccessCache = make(map[string]clientAccessData, 64)
	}
	s.clientAccessCache[cacheKey] = result
	s.clientAccessMu.Unlock()
	return result
}

// clientAccessCacheKey fingerprints the inputs of clientAccessFor. The digest
// is truncated to 16 bytes: collisions would only cause a redundant parse.
func clientAccessCacheKey(agentID string, engine core.Engine, content string) string {
	sum := sha256.Sum256([]byte(agentID + "|" + string(engine) + "|" + content))
	return agentID + "|" + string(engine) + "|" + hex.EncodeToString(sum[:16])
}

func clientAccessPageFor(agents []core.Agent, access map[string]clientAccessData, agentID string, engine core.Engine, query string) clientAccessPageData {
	result := clientAccessPageData{AgentID: strings.TrimSpace(agentID), Engine: engine, Query: strings.TrimSpace(query)}
	needle := strings.ToLower(result.Query)
	nodes := make(map[string]struct{})
	for _, agent := range agents {
		if result.AgentID != "" && agent.ID != result.AgentID {
			continue
		}
		for _, candidate := range agent.Capabilities {
			if result.Engine != "" && candidate != result.Engine {
				continue
			}
			serviceKey := deploymentKey(agent.ID, candidate)
			candidateAccess, ok := access[serviceKey]
			if !ok || len(candidateAccess.Profiles) == 0 || !clientAccessPageEntryMatches(agent, candidate, candidateAccess, needle) {
				continue
			}
			result.Entries = append(result.Entries, clientAccessPageEntry{Agent: agent, Engine: candidate, Access: candidateAccess})
			result.TotalProfiles += len(candidateAccess.Profiles)
			nodes[agent.ID] = struct{}{}
		}
	}
	result.TotalNodes = len(nodes)
	return result
}

func clientAccessPageEntryMatches(agent core.Agent, engine core.Engine, access clientAccessData, needle string) bool {
	if needle == "" {
		return true
	}
	parts := []string{agent.Name, agent.ID, string(engine), engineName(engine), access.Address, access.Source}
	for _, profile := range access.Profiles {
		parts = append(parts, profile.Tag, profile.Protocol, profile.Profile.Format)
	}
	joined := strings.ToLower(strings.Join(parts, " "))
	return strings.Contains(joined, needle)
}

func clientAddressCandidates(agent core.Agent) []clientAddressCandidate {
	result := make([]clientAddressCandidate, 0, 8)
	seen := make(map[string]struct{})
	for _, key := range []string{"public_host", "public_ip", "address"} {
		if value := strings.TrimSpace(agent.Labels[key]); value != "" {
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				result = append(result, clientAddressCandidate{Address: value, Source: key})
			}
		}
	}
	type interfaceAddress struct {
		address string
		name    string
		order   int
	}
	addresses := make([]interfaceAddress, 0, 8)
	for _, networkInterface := range agent.Metrics.NetworkInterfaces {
		for _, address := range networkInterface.Addresses {
			ip := net.ParseIP(strings.TrimSpace(address))
			if ip == nil || !ip.IsGlobalUnicast() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			priority := 2
			if ip.To4() != nil {
				priority = 0
				if ip.IsPrivate() {
					priority = 1
				}
			} else if ip.IsPrivate() {
				priority = 3
			}
			addresses = append(addresses, interfaceAddress{address: ip.String(), name: networkInterface.Name, order: priority})
		}
	}
	sort.SliceStable(addresses, func(i, j int) bool {
		if addresses[i].order != addresses[j].order {
			return addresses[i].order < addresses[j].order
		}
		if addresses[i].name != addresses[j].name {
			return addresses[i].name < addresses[j].name
		}
		return addresses[i].address < addresses[j].address
	})
	for _, item := range addresses {
		if _, exists := seen[item.address]; exists {
			continue
		}
		seen[item.address] = struct{}{}
		result = append(result, clientAddressCandidate{Address: item.address, Source: item.name})
	}
	return result
}

func firstAgentLabel(agent core.Agent, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(agent.Labels[key]); value != "" {
			return value
		}
	}
	return ""
}

func deploymentTransportName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "websocket":
		return "WebSocket"
	case "grpc":
		return "gRPC"
	default:
		return "原生传输"
	}
}

func taskTiming(task core.Task) string {
	return taskTimingAt(task, time.Now())
}

func taskTimingAt(task core.Task, now time.Time) string {
	switch task.Status {
	case core.TaskPending:
		return "准备执行"
	case core.TaskRunning:
		if task.StartedAt == nil {
			return "正在启动执行"
		}
		return "已运行 " + formatTaskDuration(nonNegativeDuration(*task.StartedAt, now))
	default:
		if task.StartedAt != nil && task.FinishedAt != nil {
			return "执行 " + formatTaskDuration(nonNegativeDuration(*task.StartedAt, *task.FinishedAt))
		}
		if task.FinishedAt != nil {
			return "未开始执行"
		}
		return "时间记录不完整"
	}
}

func nonNegativeDuration(start, end time.Time) time.Duration {
	if start.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start)
}

func formatTaskDuration(value time.Duration) string {
	if value < time.Second {
		return "不足 1 秒"
	}
	if value < time.Minute {
		return fmt.Sprintf("%d 秒", int(value/time.Second))
	}
	if value < time.Hour {
		return fmt.Sprintf("%d 分 %d 秒", int(value/time.Minute), int(value/time.Second)%60)
	}
	if value < 24*time.Hour {
		return fmt.Sprintf("%d 小时 %d 分", int(value/time.Hour), int(value/time.Minute)%60)
	}
	return fmt.Sprintf("%d 天 %d 小时", int(value/(24*time.Hour)), int(value/time.Hour)%24)
}

func sortAgentsForDisplay(agents []core.Agent) {
	sort.SliceStable(agents, func(i, j int) bool {
		if agents[i].Status != agents[j].Status {
			return agents[i].Status == "online"
		}
		return agents[i].LastSeen.After(agents[j].LastSeen)
	})
}

func selectedAgentForDisplay(requested string, agents []core.Agent) string {
	for _, agent := range agents {
		if agent.ID == requested {
			return requested
		}
	}
	if len(agents) == 0 {
		return ""
	}
	return agents[0].ID
}

func taskRetryReasons(tasks []core.Task, agents []core.Agent, configExists map[string]bool) map[string]string {
	agentExists := make(map[string]bool, len(agents))
	for _, agent := range agents {
		agentExists[agent.ID] = true
	}
	reasons := make(map[string]string)
	for _, task := range tasks {
		if task.Status != core.TaskFailed && task.Status != core.TaskCanceled {
			continue
		}
		if !agentExists[task.AgentID] {
			reasons[task.ID] = "源节点已移除，无法重试"
			continue
		}
		if task.ConfigID != "" && !configExists[task.ConfigID] {
			reasons[task.ID] = "源配置已删除，无法重试"
		}
	}
	return reasons
}

// trafficChart renders an inline SVG line chart of the download/upload rate
// history. Both series share one normalized Y axis; the newest values are
// printed next to the legend so the chart stays readable without extra JS.
func trafficChart(samples []core.MetricSample) string {
	if len(samples) < 2 {
		return ""
	}
	const (
		width  = 480.0
		height = 64.0
		pad    = 3.0
	)
	var peak uint64 = 1
	for _, sample := range samples {
		if sample.RXRateBPS > peak {
			peak = sample.RXRateBPS
		}
		if sample.TXRateBPS > peak {
			peak = sample.TXRateBPS
		}
	}
	points := func(pick func(core.MetricSample) uint64) string {
		var builder strings.Builder
		for index, sample := range samples {
			x := pad + float64(index)*(width-2*pad)/float64(len(samples)-1)
			y := height - pad - float64(pick(sample))/float64(peak)*(height-2*pad)
			if index > 0 {
				builder.WriteByte(' ')
			}
			fmt.Fprintf(&builder, "%.1f,%.1f", x, y)
		}
		return builder.String()
	}
	rxPoints := points(func(sample core.MetricSample) uint64 { return sample.RXRateBPS })
	txPoints := points(func(sample core.MetricSample) uint64 { return sample.TXRateBPS })
	last := samples[len(samples)-1]
	return fmt.Sprintf(
		`<svg class="metric-trend-chart" viewBox="0 0 %d %d" role="img" aria-label="最近 24 小时上下行速率趋势">`+
			`<polyline class="trend-line trend-rx" points="%s"></polyline>`+
			`<polyline class="trend-line trend-tx" points="%s"></polyline>`+
			`</svg><dl class="metric-trend-legend"><div><i class="trend-dot trend-rx"></i><span>下载</span><b>%s</b></div>`+
			`<div><i class="trend-dot trend-tx"></i><span>上传</span><b>%s</b></div></dl>`,
		int(width), int(height), rxPoints, txPoints, formatDataRate(last.RXRateBPS), formatDataRate(last.TXRateBPS))
}
