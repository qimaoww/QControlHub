package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (c *Client) upgradeAgent(ctx context.Context) (string, error) {
	executable := ""
	var err error
	executable, err = os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve running Agent executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve running Agent executable path: %w", err)
	}
	downloadDirectory := ""
	if executable != "" {
		downloadDirectory = filepath.Dir(executable)
	}
	temporary, version, size, err := c.downloadAgentBinary(ctx, downloadDirectory)
	if err != nil {
		return "", err
	}
	defer os.Remove(temporary)
	transaction, err := prepareAgentUpgrade(ctx, executable, temporary, version, c.executor.serviceManager().Kind())
	if err != nil {
		return "", err
	}
	if err := transaction.commit(); err != nil {
		return "", err
	}
	c.upgradeCommitted = transaction
	return fmt.Sprintf("Agent binary replaced with %s (%d bytes); reconnecting with the upgraded process", versionLabel(version), size), nil
}

// agentBinaryDownloadTimeout bounds the whole signed download, including fast
// retries, so a slow control-plane link cannot hold an upgrade task open
// forever. Each attempt shares this budget: a transfer that is progressing
// slowly is allowed to run, while a quick transient failure triggers a retry.
const agentBinaryDownloadTimeout = 6 * time.Minute

func (c *Client) downloadAgentBinary(ctx context.Context, directory string) (string, string, int64, error) {
	totalContext, cancel := context.WithTimeout(ctx, agentBinaryDownloadTimeout)
	defer cancel()
	attempts := defaultDownloadAttempts
	if attempts > maxDownloadAttempts {
		attempts = maxDownloadAttempts
	}
	retryDelay := defaultDownloadRetryDelay
	var lastErr error
	performedAttempts := 0
	for attempt := 1; attempt <= attempts; attempt++ {
		performedAttempts = attempt
		path, version, size, err := c.downloadAgentBinaryOnce(totalContext, c.http, directory)
		if err == nil {
			return path, version, size, nil
		}
		lastErr = err
		if totalContext.Err() != nil {
			return "", "", 0, totalContext.Err()
		}
		var retryable retryableCoreDownloadError
		if !errors.As(err, &retryable) || attempt == attempts {
			break
		}
		delay := retryDelay * time.Duration(1<<(attempt-1))
		timer := time.NewTimer(delay)
		select {
		case <-totalContext.Done():
			timer.Stop()
			return "", "", 0, totalContext.Err()
		case <-timer.C:
		}
	}
	if performedAttempts > 1 {
		return "", "", 0, fmt.Errorf("download Agent binary after %d attempts: %w", performedAttempts, lastErr)
	}
	return "", "", 0, lastErr
}

func (c *Client) downloadAgentBinaryOnce(ctx context.Context, client *http.Client, directory string) (string, string, int64, error) {
	privateKey, err := authn.DecodePrivateKey(c.creds.PrivateKey)
	if err != nil {
		return "", "", 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.config.ServerURL+"/agent/v1/binary", nil)
	if err != nil {
		return "", "", 0, err
	}
	if err := authn.SignRequest(request, nil, c.creds.AgentID, privateKey, time.Now().UTC()); err != nil {
		return "", "", 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", "", 0, retryableCoreDownloadError{err: explainTLSError(err)}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		contents, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		err := fmt.Errorf("control plane returned %s: %s", response.Status, strings.TrimSpace(string(contents)))
		if response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout ||
			response.StatusCode == http.StatusTooEarly || response.StatusCode == http.StatusTooManyRequests {
			return "", "", 0, retryableCoreDownloadError{err: err}
		}
		return "", "", 0, err
	}
	limit := int64(core.MaxAgentBinaryBytes)
	if response.ContentLength > limit {
		return "", "", 0, fmt.Errorf("Agent binary exceeds %d bytes", limit)
	}
	temporary, err := os.CreateTemp(directory, "qagent-upgrade-*")
	if err != nil {
		return "", "", 0, fmt.Errorf("create temporary Agent binary: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() { _ = temporary.Close(); _ = os.Remove(temporaryPath) }
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(response.Body, limit+1))
	if err != nil {
		cleanup()
		return "", "", 0, retryableCoreDownloadError{err: err}
	}
	if written == 0 || written > limit {
		cleanup()
		return "", "", 0, retryableCoreDownloadError{err: errors.New("downloaded Agent binary has an invalid size")}
	}
	if expected := strings.TrimSpace(response.Header.Get("X-QControlHub-Agent-SHA256")); len(expected) != sha256.Size*2 || !strings.EqualFold(expected, fmt.Sprintf("%x", hash.Sum(nil))) {
		cleanup()
		return "", "", 0, errors.New("downloaded Agent binary checksum mismatch")
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return "", "", 0, fmt.Errorf("sync downloaded Agent binary: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return "", "", 0, fmt.Errorf("close downloaded Agent binary: %w", err)
	}
	return temporaryPath, strings.TrimSpace(response.Header.Get("X-QControlHub-Agent-Version")), written, nil
}

func (c *Client) reexecAfterUpgrade() {
	time.Sleep(150 * time.Millisecond)
	c.taskLifecycleMu.Lock()
	defer c.taskLifecycleMu.Unlock()
	if !c.upgradePending || c.upgradeCommitted == nil {
		return
	}
	executable := c.upgradeCommitted.executable
	if digest, exists, err := protectedCoreMigrationFileDigest(executable, core.MaxAgentBinaryBytes); err != nil || !exists || digest != c.upgradeCommitted.candidateDigest {
		slog.Error("upgraded Agent changed before restart; refusing to execute it")
		return
	}
	reexec := c.reexecFunc
	if reexec == nil {
		reexec = syscall.Exec
	}
	if err := reexec(executable, os.Args, os.Environ()); err != nil {
		slog.Error("restart upgraded Agent", "error", err)
		if c.upgradeCommitted != nil {
			if rollbackErr := c.upgradeCommitted.rollback(); rollbackErr != nil {
				slog.Error("restore previous Agent executable", "error", rollbackErr)
				return
			}
			c.upgradePending = false
		}
	}
}

func (c *Client) committedUpgradeAwaitingRestart() bool {
	// Never block the websocket loop behind a long-running core mutation.
	// The next heartbeat retries if a reconnect overlaps task completion.
	if !c.taskLifecycleMu.TryLock() {
		return false
	}
	defer c.taskLifecycleMu.Unlock()
	return c.upgradePending && c.upgradeCommitted != nil
}

func versionLabel(value string) string {
	if value == "" {
		return "the current control-plane build"
	}
	return value
}

func validTaskID(value string) bool {
	if len(value) != 20 || !strings.HasPrefix(value, "tsk_") {
		return false
	}
	for _, character := range value[4:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
