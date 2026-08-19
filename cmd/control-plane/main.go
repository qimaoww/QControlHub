//go:build linux

package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/api"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

var version = "dev"

func main() {
	adminToken := requiredSecret("QCH_ADMIN_TOKEN")
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
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 20*time.Second)
	allowInsecureDatabase := envBool("QCH_ALLOW_INSECURE_DATABASE", false)
	if allowInsecureDatabase {
		slog.Warn("remote PostgreSQL certificate verification is explicitly disabled")
	}
	configEncryptionKey := strings.TrimSpace(os.Getenv("QCH_CONFIG_ENCRYPTION_KEY"))
	if configEncryptionKey != "" {
		slog.Info("configuration payloads will be encrypted at rest")
	}
	dataStore, err := store.OpenWithConfigKey(startupContext, databaseURL, allowInsecureDatabase, configEncryptionKey)
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
		slog.Info("serving enrollment-protected agent installer", "path", installerPath, "bytes", len(data))
	}

	apiServer := api.New(dataStore, api.Config{
		AdminToken:      adminToken,
		OperatorTokens:  splitList(os.Getenv("QCH_OPERATOR_TOKENS")),
		ReadonlyTokens:  splitList(os.Getenv("QCH_READONLY_TOKENS")),
		AllowedOrigins:  splitList(os.Getenv("QCH_CORS_ORIGINS")),
		SecureTransport: secureTransport,
		TrustedProxies:  trustedProxies,
		AgentBinary:     agentBinary,
		AgentInstaller:  agentInstaller,
		WebhookSecret:   strings.TrimSpace(os.Getenv("QCH_WEBHOOK_SECRET")),
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

func janitor(ctx context.Context, dataStore *store.Store) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	pruneCounter := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			operationContext, cancel := context.WithTimeout(ctx, 15*time.Second)
			if err := dataStore.RequeueStaleTasks(operationContext, 2*time.Minute, 6*time.Minute, 3); err != nil {
				slog.Error("requeue stale tasks", "error", err)
			}
			if err := dataStore.CleanupNonces(operationContext); err != nil {
				slog.Error("clean signed-request nonces", "error", err)
			}
			if _, err := dataStore.RecordAgentMetricSamples(operationContext, time.Now().UTC()); err != nil {
				slog.Warn("record agent metric samples", "error", err)
			}
			pruneCounter++
			if pruneCounter%60 == 0 {
				if _, err := dataStore.PruneMetricSamples(operationContext, time.Now().UTC().Add(-7*24*time.Hour)); err != nil {
					slog.Warn("prune old metric samples", "error", err)
				}
				if _, err := dataStore.PruneAuditLogs(operationContext, time.Now().UTC().Add(-90*24*time.Hour)); err != nil {
					slog.Warn("prune old audit logs", "error", err)
				}
			}
			cancel()
		}
	}
}

func requiredSecret(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if len(value) < 32 {
		slog.Error("security secret is missing or too short", "key", key, "minimum_bytes", 32)
		os.Exit(1)
	}
	return value
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	result := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		slog.Error("invalid boolean environment variable", "key", key, "value", value)
		os.Exit(1)
	}
	return parsed
}

func isLoopbackListen(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addressIP := net.ParseIP(host)
	return addressIP != nil && addressIP.IsLoopback()
}
