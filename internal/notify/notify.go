// Package notify delivers structured alert events to a configured webhook.
//
// Events are JSON POSTs signed with HMAC-SHA256 when a secret is configured,
// so a receiving endpoint can verify the payload came from the control plane
// (X-QControlHub-Signature: sha256=<hex>). Delivery is best-effort: failures
// are logged, never propagated to request handling.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	TypeTaskFailed   = "task.failed"
	TypeAgentOffline = "agent.offline"
	TypeAgentOnline  = "agent.online"
	TypeTrafficQuota = "traffic.quota"
)

// Event is the structured payload sent to the configured webhook.
type Event struct {
	Type       string    `json:"type"`
	Time       time.Time `json:"time"`
	AgentID    string    `json:"agent_id,omitempty"`
	Agent      string    `json:"agent,omitempty"`
	Engine     string    `json:"engine,omitempty"`
	TaskID     string    `json:"task_id,omitempty"`
	Action     string    `json:"action,omitempty"`
	Port       int       `json:"port,omitempty"`
	UsedBytes  uint64    `json:"used_bytes,omitempty"`
	LimitBytes uint64    `json:"limit_bytes,omitempty"`
	Error      string    `json:"error,omitempty"`
	Message    string    `json:"message"`
}

// Client sends signed events over HTTP. It is safe for concurrent use.
type Client struct {
	HTTP   *http.Client
	Secret string
	Log    *slog.Logger
}

func New(secret string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		HTTP:   &http.Client{Timeout: 10 * time.Second},
		Secret: secret,
		Log:    logger,
	}
}

// Send posts the event to webhookURL. An empty URL disables delivery.
// The payload is signed with the configured secret when one is set.
func (c *Client) Send(ctx context.Context, webhookURL string, event Event) error {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return nil
	}
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode webhook event: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "QControlHub/1")
	if c.Secret != "" {
		mac := hmac.New(sha256.New, []byte(c.Secret))
		_, _ = mac.Write(body)
		request.Header.Set("X-QControlHub-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("deliver webhook: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("webhook responded %s", response.Status)
	}
	return nil
}

// TaskFailedEvent builds the event for a task that finished with an error.
func TaskFailedEvent(task core.Task, agentName, errorText string) Event {
	return Event{
		Type:    TypeTaskFailed,
		Time:    time.Now().UTC(),
		AgentID: task.AgentID,
		Agent:   agentName,
		Engine:  string(task.Engine),
		TaskID:  task.ID,
		Action:  string(task.Action),
		Error:   truncate(errorText, 2000),
		Message: fmt.Sprintf("任务失败：%s 在节点 %s 上执行 %s 失败", task.Engine, agentName, task.Action),
	}
}

// AgentOfflineEvent builds the event for a node that missed its heartbeats.
func AgentOfflineEvent(agent core.Agent) Event {
	return Event{
		Type:    TypeAgentOffline,
		Time:    time.Now().UTC(),
		AgentID: agent.ID,
		Agent:   agent.Name,
		Message: fmt.Sprintf("节点 %s（%s）已离线", agent.Name, agent.ID),
	}
}

// AgentOnlineEvent builds the event for a node that resumed heartbeating.
func AgentOnlineEvent(agent core.Agent) Event {
	return Event{
		Type:    TypeAgentOnline,
		Time:    time.Now().UTC(),
		AgentID: agent.ID,
		Agent:   agent.Name,
		Message: fmt.Sprintf("节点 %s 已恢复在线", agent.Name),
	}
}

func TrafficQuotaEvent(policy core.PortTrafficPolicy, agentName string) Event {
	return Event{
		Type: TypeTrafficQuota, Time: time.Now().UTC(), AgentID: policy.AgentID, Agent: agentName,
		Engine: string(policy.Engine), Port: policy.Port, UsedBytes: policy.UsedBytes, LimitBytes: policy.LimitBytes,
		Message: fmt.Sprintf("流量配额触发：节点 %s 的 %s 端口 %d 已阻断", agentName, policy.Engine, policy.Port),
	}
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
