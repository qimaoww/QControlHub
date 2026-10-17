package agent

import (
	"context"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Challenge routes are in memory, expire independently, and are removed when
// the authenticated session ends. Existing web services are never rewritten.
type certificateChallengeService struct {
	mu      sync.RWMutex
	routes  map[string]core.HTTPChallenge
	servers []*http.Server
}

var challengeTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,256}$`)

func (c *certificateChallengeService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.NotFound(w, r)
		return
	}
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	token := strings.TrimPrefix(r.URL.Path, "/.well-known/acme-challenge/")
	if r.URL.RawQuery != "" || r.URL.RawPath != "" || !strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") || !challengeTokenPattern.MatchString(token) {
		http.NotFound(w, r)
		return
	}
	c.mu.RLock()
	route, ok := c.routes[host+"/"+token]
	c.mu.RUnlock()
	if !ok || !time.Now().Before(route.ExpiresAt) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(route.Value))
}
func (c *certificateChallengeService) Replace(ctx context.Context, routes []core.HTTPChallenge) error {
	if len(routes) > 1000 {
		return errors.New("HTTP 挑战数量超限")
	}
	next := map[string]core.HTTPChallenge{}
	for _, r := range routes {
		if !challengeTokenPattern.MatchString(r.Token) || len(r.Value) > 1024 || strings.ContainsAny(r.Domain, "/:\\\r\n\t ") || r.Domain == "" || r.OrderID == "" || r.ExpiresAt.After(time.Now().Add(20*time.Minute)) {
			return errors.New("HTTP 挑战数据无效")
		}
		if time.Now().Before(r.ExpiresAt) {
			next[strings.ToLower(r.Domain)+"/"+r.Token] = r
		}
	}
	c.mu.Lock()
	c.routes = next
	c.mu.Unlock()
	if len(next) == 0 {
		c.Close()
		return nil
	}
	if len(c.servers) > 0 {
		return nil
	}
	// 9080 is an opt-in reverse-proxy integration; port 80 is temporary and
	// only bound when free. Neither listener intercepts an existing process.
	for _, address := range []string{"127.0.0.1:9080", ":80"} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			continue
		}
		server := &http.Server{Handler: c, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096}
		c.servers = append(c.servers, server)
		go server.Serve(listener)
	}
	if len(c.servers) == 0 {
		return errors.New("无法启动 HTTP-01 验证服务；80 与 9080 均被占用")
	}
	return nil
}
func (c *certificateChallengeService) Close() {
	for _, server := range c.servers {
		_ = server.Close()
	}
	c.servers = nil
	c.mu.Lock()
	c.routes = nil
	c.mu.Unlock()
}
