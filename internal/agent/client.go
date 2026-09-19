package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type ClientConfig struct {
	ServerURL         string
	EnrollmentToken   string
	StatePath         string
	Name              string
	Version           string
	Labels            map[string]string
	Capabilities      []core.Engine
	HeartbeatEvery    time.Duration
	MetricsEvery      time.Duration
	AllowHTTP         bool
	AllowInsecureLive bool
	TLSCAFile         string
	// PublicIPProbe enables the outbound dual-stack egress probe; the interval
	// is clamped to between one minute and one day.
	PublicIPProbe      bool
	PublicIPProbeEvery time.Duration
	// PublicIPProbeIPv4Endpoints and PublicIPProbeIPv6Endpoints are the probe
	// echo URLs supplied by the operator. No third-party default is applied;
	// both empty disables the probe in NewClient.
	PublicIPProbeIPv4Endpoints []string
	PublicIPProbeIPv6Endpoints []string
}

type credentials struct {
	AgentID        string                   `json:"agent_id"`
	PrivateKey     string                   `json:"private_key"`
	Server         string                   `json:"server,omitempty"`
	CompletedTasks map[string]completedTask `json:"completed_tasks,omitempty"`
}

type completedTask struct {
	Success     bool                  `json:"success"`
	Output      string                `json:"output,omitempty"`
	Error       string                `json:"error,omitempty"`
	CompletedAt time.Time             `json:"completed_at"`
	IPQuality   *core.IPQualityResult `json:"ip_quality,omitempty"`
}

type Client struct {
	config            ClientConfig
	executor          *Executor
	http              *http.Client
	creds             credentials
	websocketURL      string
	metrics           *MetricsCollector
	traffic           *TrafficManager
	mainland          *MainlandAccessManager
	logs              *CoreLogCollector
	publicIP          *PublicIPProber
	bbr               *SystemBBRManager
	serverHost        string
	reenrollAttempted bool
	lastReenrollAt    time.Time
	credentialsMu     sync.Mutex
	executionsMu      sync.Mutex
	executions        map[string]*taskExecution
	restartAfterTask  string
	taskLifecycleMu   sync.Mutex
	upgradePending    bool
	upgradeCommitted  *agentUpgradeTransaction
	runtimeRefresh    chan struct{}
	reexecFunc        func(string, []string, []string) error
	executeFunc       func(context.Context, core.Task) (string, error)
	ipQualityFunc     func(context.Context) (core.IPQualityResult, error)

	connectionSampleMu   sync.Mutex
	nextConnectionSample time.Time
}

type taskExecution struct {
	done        chan struct{}
	result      core.TaskResultRequest
	completedAt time.Time
}

const (
	webSocketHandshakeTimeout = 30 * time.Second
	defaultHeartbeatInterval  = 15 * time.Second
	minHeartbeatInterval      = time.Second
	maxHeartbeatInterval      = 30 * time.Second
	// Metrics pushes are lightweight /proc snapshots on a dedicated wire
	// message so the panel's live card values refresh without waiting for the
	// full heartbeat cycle.
	defaultMetricsInterval = time.Second
	minMetricsInterval     = time.Second
	// maxReconnectBackoff bounds an ordinary transport reconnect so a panel
	// restart or a transient network failure recovers within seconds.
	maxReconnectBackoff = 30 * time.Second
	// identityRejectedMaxBackoff bounds a rejected-identity reconnect. A revoked
	// identity needs operator action, so once the delay saturates the Agent
	// retries slowly instead of spending the control plane's authentication
	// failure budget every 30 seconds.
	identityRejectedMaxBackoff = 10 * time.Minute
	// minReenrollInterval bounds how often a rejected identity may retry the
	// enrollment token. The control plane blocks an address after repeated failed
	// enrollments, which would also block other nodes behind the same NAT.
	minReenrollInterval = 5 * time.Minute
)

func NewClient(config ClientConfig, executor *Executor) (*Client, error) {
	if err := executor.LoadCoreMigrationState(); err != nil {
		return nil, fmt.Errorf("load existing core migration state: %w", err)
	}
	if err := executor.Validate(); err != nil {
		return nil, err
	}
	reconcileContext, reconcileCancel := context.WithTimeout(context.Background(), 20*time.Second)
	if err := executor.ReconcileExistingCoreServices(reconcileContext); err != nil {
		reconcileCancel()
		return nil, fmt.Errorf("reconcile existing core migration: %w", err)
	}
	reconcileCancel()
	logUpgradeContext, logUpgradeCancel := context.WithTimeout(context.Background(), 45*time.Second)
	if err := executor.upgradeManagedCoreLogPolicies(logUpgradeContext); err != nil {
		logUpgradeCancel()
		return nil, fmt.Errorf("upgrade managed core logging policy: %w", err)
	}
	logUpgradeCancel()
	parsed, err := url.Parse(config.ServerURL)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("QCH_SERVER_URL must be a valid absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return nil, errors.New("QCH_SERVER_URL scheme must be wss, ws, https, or http")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("QCH_SERVER_URL must be a bare origin without credentials, path, query, or fragment")
	}
	secureWebSocket := parsed.Scheme == "https" || parsed.Scheme == "wss"
	if !secureWebSocket {
		isLocal := parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1"
		if !config.AllowHTTP && !isLocal {
			return nil, errors.New("remote control-plane URL must use WSS; set QCH_ALLOW_HTTP=true only on a trusted local network")
		}
		if !isLocal && executor != nil && !config.AllowInsecureLive {
			return nil, errors.New("live task execution is forbidden over remote cleartext HTTP; set QCH_ALLOW_INSECURE_LIVE=true to explicitly allow it on a trusted network")
		}
	}
	if config.HeartbeatEvery <= 0 {
		config.HeartbeatEvery = defaultHeartbeatInterval
	} else if config.HeartbeatEvery < minHeartbeatInterval || config.HeartbeatEvery > maxHeartbeatInterval {
		return nil, errors.New("QCH_HEARTBEAT_INTERVAL must be between 1s and 30s")
	}
	if config.MetricsEvery <= 0 {
		config.MetricsEvery = defaultMetricsInterval
	} else if config.MetricsEvery < minMetricsInterval || config.MetricsEvery > maxHeartbeatInterval {
		return nil, errors.New("QCH_METRICS_INTERVAL must be between 1s and 30s")
	}
	httpScheme, websocketScheme := "http", "ws"
	if secureWebSocket {
		httpScheme, websocketScheme = "https", "wss"
	}
	config.ServerURL = httpScheme + "://" + parsed.Host
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if config.TLSCAFile != "" {
		rootCAs, err := loadTrustedCA(config.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("load QCH_TLS_CA_FILE: %w", err)
		}
		transport.TLSClientConfig.RootCAs = rootCAs
	}
	// Seed the CPU and network baselines so the first heartbeat and metrics
	// push after a process start already report usage values instead of a
	// one-sample "unavailable" reading.
	metricsCollector := NewMetricsCollector()
	warmupContext, warmupCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, _ = metricsCollector.Collect(warmupContext)
	warmupCancel()
	var ipv4ProbeEndpoints, ipv6ProbeEndpoints []string
	if config.PublicIPProbe {
		ipv4ProbeEndpoints = config.PublicIPProbeIPv4Endpoints
		ipv6ProbeEndpoints = config.PublicIPProbeIPv6Endpoints
	}
	publicIP, err := NewPublicIPProber(config.PublicIPProbeEvery, ipv4ProbeEndpoints, ipv6ProbeEndpoints)
	if err != nil {
		return nil, fmt.Errorf("invalid public IP probe configuration: %w", err)
	}
	return &Client{
		config:         config,
		executor:       executor,
		serverHost:     parsed.Host,
		websocketURL:   websocketScheme + "://" + parsed.Host + "/agent/v1/connect",
		metrics:        metricsCollector,
		traffic:        NewTrafficManagerForServiceManager(config.StatePath, executor.serviceManager()),
		mainland:       NewMainlandAccessManager(config.StatePath, executor.serviceManager()),
		logs:           NewCoreLogCollectorForExecutor(executor),
		publicIP:       publicIP,
		bbr:            NewSystemBBRManager(executor.serviceManager()),
		runtimeRefresh: make(chan struct{}, 1),
		http: &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return errors.New("control-plane redirects are disabled")
			},
		},
	}, nil
}
