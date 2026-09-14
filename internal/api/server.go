package api

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/geoip"
	"github.com/qimaoww/qcontrolhub/internal/komari"
	"github.com/qimaoww/qcontrolhub/internal/notify"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

type tokenPrincipal struct {
	Role          core.Role
	ConfigOwnerID string
	Permissions   []core.Permission
}

type liveConnection struct {
	trafficRefresh chan struct{}
	id             string
	cancel         context.CancelFunc
}

func New(dataStore *store.Store, config Config) *Server {
	origins := make(map[string]struct{}, len(config.AllowedOrigins))
	for _, origin := range config.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			origins[origin] = struct{}{}
		}
	}
	adminTokenDigest := config.AdminTokenDigest
	if adminTokenDigest == ([32]byte{}) {
		adminTokenDigest = sha256.Sum256([]byte(config.AdminToken))
	}
	roleTokens := map[[32]byte]tokenPrincipal{adminTokenDigest: {Role: core.RoleAdmin, Permissions: core.AllPermissions()}}
	for _, token := range config.OperatorTokens {
		if token = strings.TrimSpace(token); token != "" {
			roleTokens[sha256.Sum256([]byte(token))] = tokenPrincipal{Role: core.RoleUser, ConfigOwnerID: tokenConfigOwnerID(token), Permissions: legacyOperatorPermissions()}
		}
	}
	for _, token := range config.AuditorTokens {
		if token = strings.TrimSpace(token); token != "" {
			roleTokens[sha256.Sum256([]byte(token))] = tokenPrincipal{Role: core.RoleUser, ConfigOwnerID: tokenConfigOwnerID(token), Permissions: legacyAuditorPermissions()}
		}
	}
	for _, token := range config.ReadonlyTokens {
		if token = strings.TrimSpace(token); token != "" {
			roleTokens[sha256.Sum256([]byte(token))] = tokenPrincipal{Role: core.RoleUser, ConfigOwnerID: tokenConfigOwnerID(token), Permissions: legacyReadonlyPermissions()}
		}
	}
	komariClient, komariErr := komari.New(config.KomariURL, config.KomariAPIKey, config.KomariHTTPClient)
	geoipClient := geoip.New(config.GeoIPHTTPClient)
	if komariErr != nil {
		// Keep the control plane available when an optional integration is
		// misconfigured; the endpoint reports the configuration error to an
		// operator instead of preventing unrelated node management.
		komariClient = nil
	}
	komariHTTPClient := config.KomariHTTPClient
	if komariHTTPClient == nil {
		komariHTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Server{
		store:                      dataStore,
		allowedOrigins:             origins,
		secureTransport:            config.SecureTransport,
		databaseTLSVerified:        config.DatabaseTLSVerified,
		configEncryptionConfigured: config.ConfigEncryptionConfigured,
		adminLimiter:               authn.NewFailureLimiter(8, 5*time.Minute, 10*time.Minute),
		enrollLimiter:              authn.NewFailureLimiter(5, 10*time.Minute, 20*time.Minute),
		agentLimiter:               authn.NewFailureLimiter(20, time.Minute, 5*time.Minute),
		trustedProxies:             config.TrustedProxies,
		agentBinary:                config.AgentBinary,
		agentBinaryGzip:            gzipCompress(config.AgentBinary),
		agentVersion:               strings.TrimSpace(config.AgentVersion),
		controlPlaneVersion:        strings.TrimSpace(config.ControlPlaneVersion),
		agentInstaller:             config.AgentInstaller,
		publicIPProbe:              config.PublicIPProbe,
		geoip:                      geoipClient,
		komari:                     komariClient,
		komariHTTPClient:           komariHTTPClient,
		komariConfigError:          komariErr,
		webhookSigningConfigured:   strings.TrimSpace(config.WebhookSecret) != "",
		notifier:                   notify.New(config.WebhookSecret, slog.Default()),
		subStoreHTTP:               newSubStoreHTTPClient(),
		roleTokens:                 roleTokens,
		sessions:                   make(map[string]apiSession),
		sessionTTL:                 sessionTTL(config.SessionTTL),
		connections:                make(map[string]liveConnection),
	}
}
