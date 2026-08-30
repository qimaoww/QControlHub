// Package komari contains the small, read-only client used to link a
// QControlHub node with a Komari monitor node.
package komari

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxResponseBytes = 1 << 20
	requestTimeout   = 10 * time.Second
)

// Node contains the billing and traffic settings exposed by Komari. The
// effective fields are present in newer Komari releases; older releases only
// expose traffic_limit and traffic_limit_type.
type Node struct {
	UUID                     string `json:"uuid"`
	Name                     string `json:"name"`
	BillingCycle             int64  `json:"billing_cycle"`
	TrafficLimit             int64  `json:"traffic_limit"`
	TrafficLimitType         string `json:"traffic_limit_type"`
	EffectiveTrafficLimit    int64  `json:"effective_traffic_limit"`
	EffectiveTrafficType     string `json:"effective_traffic_type"`
	EffectiveTrafficLimitSet bool   `json:"-"`
	EffectiveTrafficTypeSet  bool   `json:"-"`
	TrafficResetDay          int64  `json:"traffic_reset_day"`
	ExpiredAt                string `json:"expired_at"`
	UpdatedAt                string `json:"updated_at"`
}

func (node *Node) UnmarshalJSON(data []byte) error {
	type plainNode Node
	var decoded plainNode
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var presence struct {
		EffectiveTrafficLimit json.RawMessage `json:"effective_traffic_limit"`
		EffectiveTrafficType  json.RawMessage `json:"effective_traffic_type"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return err
	}
	*node = Node(decoded)
	node.EffectiveTrafficLimitSet = len(presence.EffectiveTrafficLimit) > 0 && string(presence.EffectiveTrafficLimit) != "null"
	node.EffectiveTrafficTypeSet = len(presence.EffectiveTrafficType) > 0 && string(presence.EffectiveTrafficType) != "null"
	return nil
}

// Client calls Komari's public node API. URL is expected to be the Komari
// origin (a path is allowed for reverse-proxy deployments); APIKey is optional
// because /api/nodes is public on standard Komari installations.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New validates the configured endpoint and returns a client. An empty URL
// disables the integration and returns (nil, nil).
func New(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	if baseURL == "" {
		return nil, nil
	}
	if strings.ContainsAny(apiKey, "\r\n") {
		return nil, errors.New("Komari API Key contains invalid characters")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Komari URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	return &Client{baseURL: strings.TrimRight(parsed.String(), "/"), apiKey: apiKey, http: httpClient}, nil
}

// GetNode loads one node by UUID. The endpoint is deliberately queried as a
// list because this remains compatible with Komari versions that do not offer
// a per-node HTTP route.
func (client *Client) GetNode(ctx context.Context, uuid string) (Node, error) {
	if client == nil {
		return Node{}, errors.New("Komari integration is not configured")
	}
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return Node{}, errors.New("Komari server UUID is not configured")
	}
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, client.baseURL+"/api/nodes", nil)
	if err != nil {
		return Node{}, fmt.Errorf("create Komari request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if client.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+client.apiKey)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return Node{}, fmt.Errorf("request Komari nodes: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Node{}, fmt.Errorf("read Komari response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return Node{}, errors.New("Komari response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Node{}, fmt.Errorf("Komari returned HTTP %d", response.StatusCode)
	}
	nodes, message, err := decodeNodes(body)
	if err != nil {
		return Node{}, fmt.Errorf("decode Komari response: %w", err)
	}
	for _, node := range nodes {
		if strings.EqualFold(strings.TrimSpace(node.UUID), uuid) {
			if node.BillingCycle < 0 || node.TrafficLimit < 0 || node.EffectiveTrafficLimit < 0 || node.TrafficResetDay < 0 {
				return Node{}, errors.New("Komari returned invalid billing or traffic values")
			}
			return node, nil
		}
	}
	if message != "" {
		return Node{}, fmt.Errorf("Komari node %q was not found: %s", uuid, message)
	}
	return Node{}, fmt.Errorf("Komari node %q was not found", uuid)
}

func decodeNodes(body []byte) ([]Node, string, error) {
	var direct []Node
	if err := json.Unmarshal(body, &direct); err == nil {
		return direct, "", nil
	}
	var envelope struct {
		Status  string          `json:"status"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, "", err
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		if strings.EqualFold(envelope.Status, "error") {
			return nil, envelope.Message, nil
		}
		return nil, envelope.Message, errors.New("Komari response has no data")
	}
	if err := json.Unmarshal(envelope.Data, &direct); err != nil {
		return nil, envelope.Message, err
	}
	return direct, envelope.Message, nil
}
