//go:build linux

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/api"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

var version = "dev"

func main() {
	adminTokenDigest, err := adminTokenDigestFromEnv()
	if err != nil {
		slog.Error("invalid administrator token configuration", "error", err)
		os.Exit(1)
	}
	listenAddress := env("QCH_LISTEN", "127.0.0.1:8080")
	certFile := strings.TrimSpace(os.Getenv("QCH_TLS_CERT_FILE"))
	keyFile := strings.TrimSpace(os.Getenv("QCH_TLS_KEY_FILE"))
	if (certFile == "") != (keyFile == "") {
		slog.Error("QCH_TLS_CERT_FILE and QCH_TLS_KEY_FILE must be configured together")
		os.Exit(1)
	}
	behindTLSProxy := envBool("QCH_BEHIND_TLS_PROXY", false)
	allowInsecureHTTP := envBool("QCH_ALLOW_INSECURE_HTTP", false)
	secureTransport := certFile != "" || behindTLSProxy
	if !secureTransport && !isLoopbackListen(listenAddress) && !allowInsecureHTTP {
		slog.Error("refusing to expose authentication over cleartext HTTP", "listen", listenAddress, "hint", "configure TLS, set QCH_BEHIND_TLS_PROXY=true, or bind to loopback")
		os.Exit(1)
	}
	if allowInsecureHTTP && !secureTransport && !isLoopbackListen(listenAddress) {
		slog.Warn("INSECURE: authentication tokens will cross cleartext HTTP", "listen", listenAddress)
	}
	databaseURL := strings.TrimSpace(os.Getenv("QCH_DATABASE_URL"))
	if databaseURL == "" {
		slog.Error("QCH_DATABASE_URL is required")
		os.Exit(1)
	}
	trustedProxies, err := authn.ParseTrustedProxies(splitList(os.Getenv("QCH_TRUSTED_PROXY_CIDRS")))
	if err != nil {
		slog.Error("invalid QCH_TRUSTED_PROXY_CIDRS", "error", err)
		os.Exit(1)
	}
	probeInterval := envDuration("QCH_AGENT_PUBLIC_IP_PROBE_INTERVAL", 5*time.Minute)
	if probeInterval < time.Minute || probeInterval > 24*time.Hour {
		slog.Error("QCH_AGENT_PUBLIC_IP_PROBE_INTERVAL must be between 1m and 24h")
		os.Exit(1)
	}
	publicIPProbe := publicIPProbeConfigFromEnv(probeInterval)
	if err := publicIPProbe.Validate(); err != nil {
		slog.Error("invalid managed public IP probe configuration", "error", err)
		os.Exit(1)
	}
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 20*time.Second)
	allowInsecureDatabase := envBool("QCH_ALLOW_INSECURE_DATABASE", false)
	if allowInsecureDatabase {
		slog.Warn("remote PostgreSQL certificate verification is explicitly disabled")
	}
	configEncryptionKey, err := secretFromEnvOrFile("QCH_CONFIG_ENCRYPTION_KEY", "QCH_CONFIG_ENCRYPTION_KEY_FILE")
	if err != nil {
		slog.Error("load configuration encryption key", "error", err)
		os.Exit(1)
	}
	previousConfigEncryptionKeyList, err := secretFromEnvOrFile("QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS", "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_FILE")
	if err != nil {
		slog.Error("load previous configuration encryption keys", "error", err)
		os.Exit(1)
	}
	previousConfigEncryptionKeys := splitList(previousConfigEncryptionKeyList)
	if configEncryptionKey != "" {
		slog.Info("configuration payloads will be encrypted at rest")
	}
	dataStore, err := store.OpenWithConfigKeyring(startupContext, databaseURL, allowInsecureDatabase, configEncryptionKey, previousConfigEncryptionKeys)
	if err == nil {
		if selection := strings.TrimSpace(os.Getenv("QCH_DEFAULT_AGENT_ENGINES")); selection != "" {
			var engines []core.Engine
			engines, err = core.ParseDefaultAgentEngines(selection)
			if err == nil {
				err = dataStore.InitializeDefaultAgentEngines(startupContext, engines)
			}
		}
	}
	cancelStartup()
	if err != nil {
		slog.Error("open data store", "error", err)
		os.Exit(1)
	}
	defer dataStore.Close()

	var agentBinary []byte
	if binaryPath := strings.TrimSpace(os.Getenv("QCH_AGENT_BINARY_PATH")); binaryPath != "" {
		data, err := os.ReadFile(binaryPath)
		if err != nil {
			slog.Error("read QCH_AGENT_BINARY_PATH", "error", err)
			os.Exit(1)
		}
		agentBinary = data
		slog.Info("serving agent binary for one-click install", "path", binaryPath, "bytes", len(data))
	}
	var agentInstaller []byte
	if installerPath := strings.TrimSpace(os.Getenv("QCH_AGENT_INSTALLER_PATH")); installerPath != "" {
		data, err := os.ReadFile(installerPath)
		if err != nil {
			slog.Error("read QCH_AGENT_INSTALLER_PATH", "error", err)
			os.Exit(1)
		}
		agentInstaller = data
		slog.Info("serving add-node-credential-protected agent installer", "path", installerPath, "bytes", len(data))
	}

	apiServer := api.New(dataStore, api.Config{
		AdminTokenDigest:           adminTokenDigest,
		OperatorTokens:             splitList(os.Getenv("QCH_OPERATOR_TOKENS")),
		AuditorTokens:              splitList(os.Getenv("QCH_AUDITOR_TOKENS")),
		ReadonlyTokens:             splitList(os.Getenv("QCH_READONLY_TOKENS")),
		AllowedOrigins:             splitList(os.Getenv("QCH_CORS_ORIGINS")),
		SecureTransport:            secureTransport,
		DatabaseTLSVerified:        !allowInsecureDatabase,
		ConfigEncryptionConfigured: configEncryptionKey != "",
		TrustedProxies:             trustedProxies,
		AgentBinary:                agentBinary,
		AgentVersion:               version,
		ControlPlaneVersion:        version,
		AgentInstaller:             agentInstaller,
		WebhookSecret:              strings.TrimSpace(os.Getenv("QCH_WEBHOOK_SECRET")),
		KomariURL:                  strings.TrimSpace(os.Getenv("QCH_KOMARI_URL")),
		KomariAPIKey:               strings.TrimSpace(os.Getenv("QCH_KOMARI_API_KEY")),
		PublicIPProbe:              publicIPProbe,
	})
	root := apiServer.Handler()

	server := &http.Server{
		Addr:              listenAddress,
		Handler:           root,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go janitor(ctx, dataStore)
	go cleanDeletedAgents(ctx, dataStore)
	go apiServer.MonitorAgentPresence(ctx)
	go apiServer.MonitorPanelMetrics(ctx)
	startDiagnosticListener(ctx)
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			slog.Error("graceful shutdown", "error", err)
		}
	}()

	slog.Info("QControlHub control plane starting", "version", version, "listen", server.Addr, "tls", certFile != "", "behind_tls_proxy", behindTLSProxy)
	if certFile != "" {
		err = server.ListenAndServeTLS(certFile, keyFile)
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("control plane stopped", "error", err)
		os.Exit(1)
	}
}
