package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

const subStoreResponseLimit = 1 << 20

type subStoreSyncProfile struct {
	AgentID     string      `json:"agent_id"`
	AgentName   string      `json:"agent_name"`
	Engine      core.Engine `json:"engine"`
	ProfileTag  string      `json:"profile_tag"`
	Protocol    string      `json:"protocol"`
	DefaultName string      `json:"default_name"`
	CustomName  string      `json:"custom_name,omitempty"`
	Selected    bool        `json:"selected"`
	Available   bool        `json:"available"`
	URI         string      `json:"-"`
}

func (profile subStoreSyncProfile) key() string {
	return profile.AgentID + "\x00" + string(profile.Engine) + "\x00" + profile.ProfileTag
}

type subStoreSyncResource struct {
	Settings   core.SubStoreSyncSettings    `json:"settings"`
	Profiles   []subStoreSyncProfile        `json:"profiles"`
	Selections []core.SubStoreSyncSelection `json:"selections"`
}

type subStoreSettingsRequest struct {
	EndpointURL      string `json:"endpoint_url"`
	SubscriptionName string `json:"subscription_name"`
}

type subStoreSelectionsRequest struct {
	Selections []core.SubStoreSyncSelection `json:"selections"`
}

type subStoreSyncResult struct {
	SubscriptionName string    `json:"subscription_name"`
	NodeCount        int       `json:"node_count"`
	Created          bool      `json:"created"`
	SyncedAt         time.Time `json:"synced_at"`
}

type subStoreEnvelope struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details string `json:"details"`
	} `json:"error"`
}

func newSubStoreHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			// The backend path is a credential. Never expose it to an ambient
			// HTTP proxy configured for the control-plane process.
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 8 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("Sub-Store redirects are not allowed")
		},
	}
}

func normalizeSubStoreEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || utf8.RuneCountInString(raw) > 1000 {
		return "", errors.New("Sub-Store 地址不能为空且不能超过 1000 个字符")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("Sub-Store 地址必须是包含路径口令的 HTTP(S) 绝对地址")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Sub-Store 地址不能包含用户信息、查询参数或片段")
	}
	if strings.EqualFold(parsed.Hostname(), "sub.store") {
		return "", errors.New("sub.store 不是官方公网后端域名，请填写自建 Sub-Store 地址")
	}
	cleanPath := path.Clean(parsed.EscapedPath())
	if cleanPath == "." || cleanPath == "/" || strings.Contains(cleanPath, "%") {
		return "", errors.New("Sub-Store 地址必须包含未编码的后端路径口令")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(cleanPath, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("Sub-Store 后端路径口令无效")
		}
		for _, character := range segment {
			if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
				(character >= '0' && character <= '9') || strings.ContainsRune("-._~", character)) {
				return "", errors.New("Sub-Store 后端路径口令只能使用字母、数字、-、_、. 或 ~")
			}
		}
	}
	parsed.Path = cleanPath
	parsed.RawPath = ""
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func subStoreEndpointHint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return "已保护保存"
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	masked := make([]string, 0, len(segments))
	for range segments {
		masked = append(masked, "••••••")
	}
	return parsed.Scheme + "://" + parsed.Host + "/" + strings.Join(masked, "/")
}

func (s *Server) getSubStoreSync(w http.ResponseWriter, request *http.Request) {
	resource, err := s.subStoreSyncResource(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

func (s *Server) subStoreSyncResource(ctx context.Context) (subStoreSyncResource, error) {
	settings, err := s.store.SubStoreSyncSettings(ctx)
	if err != nil {
		return subStoreSyncResource{}, err
	}
	if settings.Configured {
		settings.EndpointHint = subStoreEndpointHint(settings.EndpointURL)
	}
	selections, err := s.store.ListSubStoreSyncSelections(ctx)
	if err != nil {
		return subStoreSyncResource{}, err
	}
	profiles, err := s.availableSubStoreProfiles(ctx, selections)
	if err != nil {
		return subStoreSyncResource{}, err
	}
	return subStoreSyncResource{Settings: settings, Profiles: profiles, Selections: selections}, nil
}

func (s *Server) availableSubStoreProfiles(ctx context.Context, selections []core.SubStoreSyncSelection) ([]subStoreSyncProfile, error) {
	entries, err := s.clientAccessEntries(ctx)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]core.SubStoreSyncSelection, len(selections))
	for _, selection := range selections {
		selected[selection.Key()] = selection
	}
	profiles := make([]subStoreSyncProfile, 0)
	available := make(map[string]struct{})
	for _, entry := range entries {
		for _, item := range entry.Profiles {
			profile := subStoreSyncProfile{
				AgentID: entry.AgentID, AgentName: entry.AgentName, Engine: entry.Engine,
				ProfileTag: item.Tag, Protocol: item.Protocol, URI: item.Profile.URI, Available: true,
				DefaultName: strings.TrimSpace(entry.AgentName + " · " + item.Tag),
			}
			if selection, ok := selected[profile.key()]; ok {
				profile.Selected = true
				profile.CustomName = selection.CustomName
			}
			available[profile.key()] = struct{}{}
			profiles = append(profiles, profile)
		}
	}
	for _, selection := range selections {
		if _, ok := available[selection.Key()]; ok {
			continue
		}
		profiles = append(profiles, subStoreSyncProfile{
			AgentID: selection.AgentID, Engine: selection.Engine, ProfileTag: selection.ProfileTag,
			DefaultName: selection.CustomName, CustomName: selection.CustomName, Selected: true, Available: false,
		})
	}
	sort.SliceStable(profiles, func(left, right int) bool {
		if profiles[left].AgentName != profiles[right].AgentName {
			return profiles[left].AgentName < profiles[right].AgentName
		}
		if profiles[left].Engine != profiles[right].Engine {
			return profiles[left].Engine < profiles[right].Engine
		}
		return profiles[left].ProfileTag < profiles[right].ProfileTag
	})
	return profiles, nil
}

func (s *Server) putSubStoreSettings(w http.ResponseWriter, request *http.Request) {
	var input subStoreSettingsRequest
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	current, err := s.store.SubStoreSyncSettings(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	endpoint := strings.TrimSpace(input.EndpointURL)
	if endpoint == "" && current.Configured {
		endpoint = current.EndpointURL
	}
	endpoint, err = normalizeSubStoreEndpoint(endpoint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	settings, err := s.store.SaveSubStoreSyncSettings(request.Context(), endpoint, input.SubscriptionName)
	if err != nil {
		if errors.Is(err, store.ErrSecretUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "请先配置 QCH_CONFIG_ENCRYPTION_KEY，再保存 Sub-Store 地址")
			return
		}
		writeStoreError(w, err)
		return
	}
	settings.EndpointHint = subStoreEndpointHint(settings.EndpointURL)
	s.recordAudit(request, "substore.settings.updated", settings.SubscriptionName, "Sub-Store connection settings updated")
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) putSubStoreSelections(w http.ResponseWriter, request *http.Request) {
	var input subStoreSelectionsRequest
	if err := decodeJSON(w, request, &input, 128<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	profiles, err := s.availableSubStoreProfiles(request.Context(), nil)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	available := make(map[string]subStoreSyncProfile, len(profiles))
	for _, profile := range profiles {
		available[profile.key()] = profile
	}
	for index := range input.Selections {
		profile, ok := available[input.Selections[index].Key()]
		if !ok || !profile.Available {
			writeError(w, http.StatusBadRequest, "选择中包含已不可用的客户端节点，请刷新后重试")
			return
		}
		if strings.TrimSpace(input.Selections[index].CustomName) == "" {
			input.Selections[index].CustomName = profile.DefaultName
		}
	}
	selections, err := s.store.ReplaceSubStoreSyncSelections(request.Context(), input.Selections)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "substore.nodes.updated", "Sub-Store", fmt.Sprintf("selected node count: %d", len(selections)))
	writeJSON(w, http.StatusOK, selections)
}

func (s *Server) testSubStoreConnection(w http.ResponseWriter, request *http.Request) {
	settings, err := s.store.SubStoreSyncSettings(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !settings.Configured {
		writeError(w, http.StatusBadRequest, "请先保存 Sub-Store 连接设置")
		return
	}
	var environment map[string]any
	if _, err := s.subStoreRequest(request.Context(), settings.EndpointURL, http.MethodGet, "/api/utils/env", nil, &environment); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "endpoint_hint": subStoreEndpointHint(settings.EndpointURL)})
}

func (s *Server) runSubStoreSync(w http.ResponseWriter, request *http.Request) {
	settings, err := s.store.SubStoreSyncSettings(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !settings.Configured {
		writeError(w, http.StatusBadRequest, "请先保存 Sub-Store 连接设置")
		return
	}
	selections, err := s.store.ListSubStoreSyncSelections(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if len(selections) == 0 {
		writeError(w, http.StatusBadRequest, "请至少选择一个客户端节点")
		return
	}
	profiles, err := s.availableSubStoreProfiles(request.Context(), selections)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	byKey := make(map[string]subStoreSyncProfile, len(profiles))
	for _, profile := range profiles {
		if profile.Available {
			byKey[profile.key()] = profile
		}
	}
	content := make([]string, 0, len(selections))
	for _, selection := range selections {
		profile, ok := byKey[selection.Key()]
		if !ok {
			err = errors.New("已选客户端节点发生变化，请删除失效项或重新选择后再同步")
			_ = s.store.RecordSubStoreSyncResult(request.Context(), err)
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		renamed, renameErr := renameSubStoreNode(profile.URI, selection.CustomName)
		if renameErr != nil {
			err = fmt.Errorf("客户端节点 %s 无法同步: %w", selection.CustomName, renameErr)
			_ = s.store.RecordSubStoreSyncResult(request.Context(), err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		content = append(content, renamed)
	}
	created, syncErr := s.upsertSubStoreSubscription(request.Context(), settings, strings.Join(content, "\n"))
	if recordErr := s.store.RecordSubStoreSyncResult(request.Context(), syncErr); recordErr != nil && syncErr == nil {
		syncErr = recordErr
	}
	if syncErr != nil {
		writeError(w, http.StatusBadGateway, syncErr.Error())
		return
	}
	result := subStoreSyncResult{SubscriptionName: settings.SubscriptionName, NodeCount: len(content), Created: created, SyncedAt: time.Now().UTC()}
	s.recordAudit(request, "substore.synced", settings.SubscriptionName, fmt.Sprintf("node count: %d", len(content)))
	writeJSON(w, http.StatusOK, result)
}

func renameSubStoreNode(rawURI, name string) (string, error) {
	rawURI = strings.TrimSpace(rawURI)
	name = strings.TrimSpace(name)
	if rawURI == "" || strings.ContainsAny(rawURI, "\r\n") {
		return "", errors.New("客户端 URI 无效")
	}
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsAny(name, "\r\n") {
		return "", errors.New("节点名称不能为空且不能超过 100 个字符")
	}
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme == "" {
		return "", errors.New("客户端 URI 无效")
	}
	if fragment := strings.IndexByte(rawURI, '#'); fragment >= 0 {
		rawURI = rawURI[:fragment]
	}
	return rawURI + "#" + name, nil
}

func (s *Server) upsertSubStoreSubscription(ctx context.Context, settings core.SubStoreSyncSettings, content string) (bool, error) {
	payload := map[string]any{
		"name":                       settings.SubscriptionName,
		"displayName":                settings.SubscriptionName,
		"display-name":               settings.SubscriptionName,
		"source":                     "local",
		"content":                    content,
		"noFlow":                     true,
		"remark":                     "Managed by QControlHub",
		"process":                    []any{},
		"qcontrolhub_integration_id": settings.IntegrationID,
	}
	var subscriptions []map[string]any
	if _, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodGet, "/api/subs", nil, &subscriptions); err != nil {
		return false, err
	}
	var existing map[string]any
	for _, subscription := range subscriptions {
		name, _ := subscription["name"].(string)
		if name == settings.SubscriptionName {
			existing = subscription
			break
		}
	}
	if existing == nil {
		_, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodPost, "/api/subs", payload, nil)
		return true, err
	}
	owner, _ := existing["qcontrolhub_integration_id"].(string)
	if owner != settings.IntegrationID {
		return false, errors.New("Sub-Store 中已存在同名订阅，但它不是由当前 QControlHub 同步创建；请更换订阅名称")
	}
	_, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodPatch, "/api/sub/"+settings.SubscriptionName, payload, nil)
	return false, err
}

func (s *Server) subStoreRequest(ctx context.Context, endpoint, method, route string, payload any, destination any) (int, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return 0, errors.New("Sub-Store 地址无效")
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + route
	var body io.Reader
	if payload != nil {
		encoded, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			return 0, errors.New("无法编码 Sub-Store 同步请求")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, base.String(), body)
	if err != nil {
		return 0, errors.New("无法创建 Sub-Store 请求")
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := s.subStoreHTTP
	if client == nil {
		client = newSubStoreHTTPClient()
	}
	response, err := client.Do(req)
	if err != nil {
		// url.Error includes the complete request URL. The Sub-Store backend
		// path is a credential, so never return or persist that error verbatim.
		return 0, errors.New("连接 Sub-Store 失败，请确认地址和网络")
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, subStoreResponseLimit+1)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return response.StatusCode, errors.New("读取 Sub-Store 响应失败")
	}
	if len(contents) > subStoreResponseLimit {
		return response.StatusCode, errors.New("Sub-Store 响应超过安全大小限制")
	}
	var envelope subStoreEnvelope
	if len(contents) > 0 && json.Unmarshal(contents, &envelope) != nil {
		return response.StatusCode, fmt.Errorf("Sub-Store 返回了无效响应 (%s)", response.Status)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || envelope.Status != "success" {
		message := strings.TrimSpace(envelope.Error.Message)
		if message == "" {
			message = strings.TrimSpace(envelope.Error.Details)
		}
		message = strings.ReplaceAll(strings.ReplaceAll(message, "\r", " "), "\n", " ")
		if len(message) > 240 {
			message = message[:240]
		}
		if message == "" {
			message = response.Status
		}
		return response.StatusCode, fmt.Errorf("Sub-Store 请求失败: %s", message)
	}
	if destination != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, destination); err != nil {
			return response.StatusCode, errors.New("Sub-Store 数据响应格式无效")
		}
	}
	return response.StatusCode, nil
}
