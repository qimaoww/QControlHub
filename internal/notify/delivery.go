package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

// DeliveryError exposes only fixed diagnostics, never remote bodies or target URLs.
type DeliveryError struct {
	Reason string
	Retry  bool
	After  time.Duration
}

func (e *DeliveryError) Error() string { return e.Reason }

type Sender struct {
	RootCAs *x509.CertPool
	HTTP    *http.Client
	Dial    func(context.Context, string, string) (net.Conn, error)
}

func NewSender() *Sender {
	allowed := []netip.Prefix{}
	for _, raw := range strings.Split(os.Getenv("QCH_NOTIFICATION_ALLOWED_CIDRS"), ",") {
		if p, e := netip.ParsePrefix(strings.TrimSpace(raw)); e == nil {
			allowed = append(allowed, p)
		}
	}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("无效通知目标")
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("通知目标 DNS 解析失败")
		}
		for _, ip := range ips {
			ok := netpolicy.IsPublicAddress(ip)
			for _, p := range allowed {
				if p.Contains(ip.Unmap()) {
					ok = true
				}
			}
			if !ok {
				return nil, errors.New("通知目标不在部署允许的网络范围")
			}
		}
		return (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &Sender{Dial: dial, HTTP: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("通知禁止重定向") }, Transport: &http.Transport{Proxy: nil, DialContext: dial, MaxConnsPerHost: 4, MaxIdleConns: 16, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}}
}
func (s *Sender) Send(ctx context.Context, c Channel, e Event) error {
	if c.Type == "smtp" {
		return s.sendMail(ctx, c, e)
	}
	target := c.Get("url")
	text := e.Title + "\n" + e.Message
	payload := any(e)
	headers := map[string]string{}
	switch c.Type {
	case "webhook":
		if a := c.Get("authorization"); a != "" {
			headers["Authorization"] = a
		}
	case "telegram":
		target = "https://api.telegram.org/bot" + url.PathEscape(c.Get("token")) + "/sendMessage"
		p := map[string]any{"chat_id": c.Get("chat_id"), "text": Clip(text, 4000)}
		for _, key := range []string{"message_thread_id", "direct_messages_topic_id"} {
			if v := c.Get(key); v != "" {
				n, _ := strconv.ParseInt(v, 10, 64)
				p[key] = n
			}
		}
		payload = p
	case "bark":
		target = strings.TrimRight(target, "/") + "/push"
		payload = map[string]any{"device_key": c.Get("device_key"), "title": Clip(e.Title, 200), "body": Clip(e.Message, 3000), "group": "QControlHub"}
	case "ntfy":
		payload = map[string]any{"topic": c.Get("topic"), "title": Clip(e.Title, 200), "message": Clip(e.Message, 3500)}
		if c.Get("token") != "" {
			headers["Authorization"] = "Bearer " + c.Get("token")
		}
	case "gotify":
		target = strings.TrimRight(target, "/") + "/message"
		headers["X-Gotify-Key"] = c.Get("token")
		payload = map[string]any{"title": Clip(e.Title, 200), "message": Clip(e.Message, 3500), "priority": 5}
	default:
		return &DeliveryError{Reason: "不支持的通知渠道"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return &DeliveryError{Reason: "消息编码失败"}
	}
	req, err := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(body))
	if err != nil {
		return &DeliveryError{Reason: "通知地址无效"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "QControlHub/1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.Type == "webhook" && c.Get("secret") != "" {
		mac := hmac.New(sha256.New, []byte(c.Get("secret")))
		mac.Write(body)
		req.Header.Set("X-QControlHub-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return &DeliveryError{Reason: "通知连接失败或超时", Retry: true}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil {
		return &DeliveryError{Reason: "通知响应读取失败", Retry: true}
	}
	if len(data) > 65536 {
		return &DeliveryError{Reason: "通知响应过大"}
	}
	after := time.Duration(0)
	if n, _ := strconv.Atoi(resp.Header.Get("Retry-After")); n > 0 {
		after = time.Duration(n) * time.Second
	} else if at, er := http.ParseTime(resp.Header.Get("Retry-After")); er == nil {
		after = time.Until(at)
	}
	if after > 24*time.Hour {
		after = 24 * time.Hour
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &DeliveryError{Reason: fmt.Sprintf("平台 HTTP %d", resp.StatusCode), Retry: resp.StatusCode == 429 || resp.StatusCode >= 500, After: after}
	}
	if c.Type == "webhook" {
		return nil
	}
	var result struct {
		OK         *bool           `json:"ok"`
		Code       *int            `json:"code"`
		ErrCode    *int            `json:"errcode"`
		ID         json.RawMessage `json:"id"`
		Event      string          `json:"event"`
		ErrorCode  int             `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(data, &result) != nil {
		return &DeliveryError{Reason: "平台响应无效"}
	}
	success := false
	code := 0
	switch c.Type {
	case "telegram":
		success = result.OK != nil && *result.OK
		code = result.ErrorCode
		after = time.Duration(result.Parameters.RetryAfter) * time.Second
	case "bark":
		success = result.Code != nil && *result.Code == 200
		if result.Code != nil {
			code = *result.Code
		}
	case "gotify":
		success = len(result.ID) > 0 && string(result.ID) != "null"
	case "ntfy":
		success = result.Event == "message" && len(result.ID) > 0
	}
	if !success {
		return &DeliveryError{Reason: fmt.Sprintf("平台拒绝消息（%d）", code), Retry: code == 429 || code >= 500 && code <= 599, After: after}
	}
	return nil
}
func (s *Sender) sendMail(ctx context.Context, c Channel, e Event) error {
	host := c.Get("host")
	port, err := strconv.Atoi(c.Get("port"))
	if err != nil || port < 1 || port > 65535 {
		return &DeliveryError{Reason: "SMTP 端口无效"}
	}
	conn, err := s.Dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return &DeliveryError{Reason: "SMTP 连接失败", Retry: true}
	}
	defer conn.Close()
	deadline := time.Now().Add(12 * time.Second)
	if t, ok := ctx.Deadline(); ok && t.Before(deadline) {
		deadline = t
	}
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	cfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: s.RootCAs}
	if c.Get("tls") == "tls" {
		secure := tls.Client(conn, cfg)
		if err := secure.HandshakeContext(ctx); err != nil {
			return &DeliveryError{Reason: "SMTP TLS 失败"}
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return &DeliveryError{Reason: "SMTP 握手失败", Retry: true}
	}
	defer client.Close()
	if c.Get("tls") == "starttls" {
		if err := client.StartTLS(cfg); err != nil {
			return &DeliveryError{Reason: "SMTP STARTTLS 失败"}
		}
	}
	if user := c.Get("username"); user != "" {
		if err := client.Auth(smtp.PlainAuth("", user, c.Get("password"), host)); err != nil {
			return &DeliveryError{Reason: "SMTP 认证失败"}
		}
	}
	if err := client.Mail(c.Get("from")); err != nil {
		return smtpFailure(err)
	}
	if err := client.Rcpt(c.Get("to")); err != nil {
		return smtpFailure(err)
	}
	writer, err := client.Data()
	if err != nil {
		return smtpFailure(err)
	}
	title := strings.NewReplacer("\r", " ", "\n", " ").Replace(e.Title)
	content := "From: " + c.Get("from") + "\r\nTo: " + c.Get("to") + "\r\nSubject: " + mime.BEncoding.Encode("UTF-8", title) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(e.Message))
	for len(encoded) > 76 {
		content += encoded[:76] + "\r\n"
		encoded = encoded[76:]
	}
	content += encoded + "\r\n"
	if _, err := io.WriteString(writer, content); err != nil {
		return smtpFailure(err)
	}
	if err := writer.Close(); err != nil {
		return smtpFailure(err)
	}
	return nil
}
func smtpFailure(err error) error {
	retry := !strings.HasPrefix(err.Error(), "5")
	return &DeliveryError{Reason: "SMTP 未接受消息", Retry: retry}
}
