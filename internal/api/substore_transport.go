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
	"strings"
	"time"
	"unicode/utf8"
)

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
