package api

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/geoip"
	"github.com/qimaoww/qcontrolhub/internal/komari"
	"github.com/qimaoww/qcontrolhub/internal/notify"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

type Config struct {
	AdminToken                 string
	AdminTokenDigest           [32]byte
	OperatorTokens             []string
	AuditorTokens              []string
	ReadonlyTokens             []string
	AllowedOrigins             []string
	SecureTransport            bool
	DatabaseTLSVerified        bool
	ConfigEncryptionConfigured bool
	TrustedProxies             []*net.IPNet
	AgentBinary                []byte
	AgentVersion               string
	ControlPlaneVersion        string
	AgentInstaller             []byte
	WebhookSecret              string
	PublicIPProbe              core.PublicIPProbeConfig
	KomariURL                  string
	KomariAPIKey               string
	KomariHTTPClient           *http.Client
	GeoIPHTTPClient            *http.Client
	SessionTTL                 time.Duration
}

type Server struct {
	store                      *store.Store
	allowedOrigins             map[string]struct{}
	secureTransport            bool
	databaseTLSVerified        bool
	configEncryptionConfigured bool
	adminLimiter               *authn.FailureLimiter
	enrollLimiter              *authn.FailureLimiter
	agentLimiter               *authn.FailureLimiter
	trustedProxies             []*net.IPNet
	agentBinary                []byte
	agentBinaryGzip            []byte
	agentVersion               string
	controlPlaneVersion        string
	agentInstaller             []byte
	publicIPProbe              core.PublicIPProbeConfig
	geoip                      *geoip.Client
	komari                     *komari.Client
	komariHTTPClient           *http.Client
	komariConfigError          error
	komariCacheMu              sync.Mutex
	komariCache                map[string]cachedKomariNode
	webhookSigningConfigured   bool
	notifier                   *notify.Client
	subStoreHTTP               *http.Client
	ipQualityArchiveHTTP       *http.Client
	roleTokens                 map[[32]byte]tokenPrincipal
	sessionsMu                 sync.Mutex
	sessions                   map[string]apiSession
	sessionTTL                 time.Duration
	connectionsMu              sync.Mutex
	connections                map[string]liveConnection
	panelMetricsMu             sync.RWMutex
	panelMetrics               core.HostMetrics
	auditWriter                func(context.Context, core.AuditLogEntry) error
}
